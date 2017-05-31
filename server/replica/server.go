package replica

import (
	"sync"

	"github.com/privacylab/talek/common"
	"github.com/privacylab/talek/pir/pirinterface"
	"github.com/privacylab/talek/protocol/layout"
	"github.com/privacylab/talek/protocol/notify"
	"github.com/privacylab/talek/protocol/replica"
	"github.com/privacylab/talek/server"
	"golang.org/x/net/trace"
)

// Server is the main logic for replicas
type Server struct {
	/** Private State **/
	// Static
	log        *common.Logger
	name       string
	addr       string
	networkRPC *server.NetworkRPC
	config     common.Config // Config
	group      uint64
	pirBacking string

	// Thread-safe (organized by lock scope)
	lock         *sync.RWMutex
	snapshotID   uint64
	layoutAddr   string
	layoutClient *layout.Client
	shards       []pirinterface.Shard

	msgLock  *sync.Mutex
	messages map[uint64]*common.WriteArgs
}

// NewServer creates a new replica server
func NewServer(name string, addr string, listenRPC bool, config common.Config, group uint64, pirBacking string) (*Server, error) {
	s := &Server{}
	s.log = common.NewLogger(name)
	s.name = name
	s.addr = addr
	s.networkRPC = nil
	if listenRPC {
		s.networkRPC = server.NewNetworkRPCAddr(s, addr)
	}
	s.config = config
	s.group = group
	s.pirBacking = pirBacking

	s.lock = &sync.RWMutex{}
	s.snapshotID = 0
	s.layoutAddr = ""
	s.layoutClient = layout.NewClient(s.name, "")
	s.shards = make([]pirinterface.Shard, s.config.NumShardsPerGroup)

	s.msgLock = &sync.Mutex{}
	s.messages = make(map[uint64]*common.WriteArgs)

	s.log.Info.Printf("replica.NewServer(%v) success\n", name)
	return s, nil
}

/**********************************
 * PUBLIC RPC METHODS (threadsafe)
 **********************************/

// GetInfo returns information about this server
func (s *Server) GetInfo(args *interface{}, reply *replica.GetInfoReply) error {
	tr := trace.New("Replica", "GetInfo")
	defer tr.Finish()
	s.lock.RLock()

	reply.Err = ""
	reply.Name = s.name
	reply.SnapshotID = s.snapshotID

	s.lock.RUnlock()
	return nil
}

// Notify this server of a new snapshotID
func (s *Server) Notify(args *notify.Args, reply *notify.Reply) error {
	tr := trace.New("Replica", "Notify")
	defer tr.Finish()
	//s.lock.RLock()

	go s.GetLayout(args.Addr, args.SnapshotID)
	reply.Err = ""

	//s.lock.RUnlock()
	return nil
}

// Write stores a single message
func (s *Server) Write(args *common.WriteArgs, reply *common.WriteReply) error {
	tr := trace.New("Replica", "Write")
	defer tr.Finish()
	// Check that the data size is correct before accepting
	if uint64(len(args.Data)) != s.config.DataSize {
		reply.Err = "Invalid data size"
		return nil
	}

	s.msgLock.Lock()

	s.messages[args.ID] = args
	reply.Err = ""

	s.msgLock.Unlock()
	return nil
}

// Read a batch of requests for a shard range
func (s *Server) Read(args *replica.ReadArgs, reply *replica.ReadReply) error {
	tr := trace.New("Replica", "Read")
	defer tr.Finish()
	s.lock.RLock()

	if s.snapshotID < args.SnapshotID {
		go s.GetLayout(s.layoutAddr, args.SnapshotID)
		reply.Err = "Need updated layout. Try again later."
		s.lock.RUnlock()
		return nil
	}

	// @todo
	//shardIdx :=
	//shard := s.shards[shardIdx]
	reply.Err = ""

	s.lock.RUnlock()
	return nil
}

/**********************************
 * PUBLIC LOCAL METHODS (threadsafe)
 **********************************/

// Close shuts down the server
func (s *Server) Close() {
	s.log.Info.Printf("%v.Close: success\n", s.name)
	//s.lock.Lock()

	if s.networkRPC != nil {
		s.networkRPC.Kill()
		s.networkRPC = nil
	}

	//s.lock.Unlock()
}

// SetLayoutAddr will set the address and RPC client towards the server from which we get layouts
// Note: This will do nothing if addr is the same as we've seen before
func (s *Server) SetLayoutAddr(addr string) {
	// Check if layoutAddr has changed
	s.lock.RLock()
	if s.layoutAddr == addr {
		s.lock.RUnlock()
		return
	}
	s.lock.RUnlock()

	// Setup a new RPC client
	s.lock.Lock()
	if s.layoutClient != nil {
		s.layoutClient.Close()
	}
	s.layoutAddr = addr
	s.layoutClient = layout.NewClient(s.name, addr)
	s.log.Info.Printf("%v.SetLayoutAddr(%v): success\n", s.name, addr)
	s.lock.Unlock()
}

// GetLayout will fetch the layout for a snapshotID and apply it locally
func (s *Server) GetLayout(addr string, snapshotID uint64) {
	tr := trace.New("Replica", "GetLayout")
	defer tr.Finish()

	// Try to establish an RPC client to server. Does nothing if addr is seen before
	s.SetLayoutAddr(addr)
	// Locked region
	s.lock.Lock()

	// Do RPC
	layoutSize := s.config.NumBucketsPerShard * s.config.NumShardsPerGroup
	var reply layout.GetLayoutReply
	args := &layout.GetLayoutArgs{
		SnapshotID: snapshotID,
		Index:      s.group,
		NumSplit:   s.config.NumBuckets / layoutSize,
	}
	err := s.layoutClient.GetLayout(args, &reply)

	// Error handling
	if err != nil {
		s.log.Error.Printf("%v.GetLayout(%v, %v) returns error: %v, giving up.\n", s.name, addr, snapshotID, err)
		s.lock.Unlock()
		return
	} else if reply.Err == layout.ErrorInvalidSnapshotID {
		s.log.Error.Printf("%v.GetLayout(%v, %v) failed with invalid SnapshotID=%v, should be %v. Trying again.\n", s.name, addr, snapshotID, args.SnapshotID, reply.SnapshotID)
		go s.GetLayout(addr, reply.SnapshotID)
		s.lock.Unlock()
		return
	} else if reply.Err == layout.ErrorInvalidIndex {
		s.log.Error.Printf("%v.GetLayout(%v, %v) failed with invalid Index=%v, giving up.\n", s.name, addr, snapshotID, args.Index)
		s.lock.Unlock()
		return
	} else if reply.Err == layout.ErrorInvalidNumSplit {
		s.log.Error.Printf("%v.GetLayout(%v, %v) failed with invalid NumSplit=%v, giving up.\n", s.name, addr, snapshotID, args.NumSplit)
		s.lock.Unlock()
		return
	}

	// Only set on success
	shards := s.ApplyLayout(s.config, s.pirBacking, reply.Layout)
	if shards != nil {
		s.snapshotID = snapshotID
		s.shards = shards
	}

	s.lock.Unlock()
}

// ApplyLayout takes in a new layout and generates Shards from previously stored bank of messages
func (s *Server) ApplyLayout(config common.Config, pirBacking string, layout []uint64) []pirinterface.Shard {
	// Build shards
	s.msgLock.Lock()
	shards := make([]pirinterface.Shard, config.NumShardsPerGroup)
	NewShard := pirinterface.GetBacking(pirBacking)
	bucketSize := int(config.BucketDepth * config.DataSize)
	itemsPerShard := config.NumBucketsPerShard * config.BucketDepth
	shardSize := itemsPerShard * config.DataSize
	for i := uint64(0); i < config.NumShardsPerGroup; i++ {
		data := make([]byte, shardSize)
		for j := uint64(0); j < itemsPerShard; j++ {
			layoutIdx := i*itemsPerShard + j
			dataIdx := j * config.DataSize
			id := layout[layoutIdx]
			msg, ok := s.messages[id]
			if !ok {
				s.log.Error.Printf("ApplyLayout() failed. Missing message ID=%v, giving up.\n", id)
				s.lock.Unlock()
				return nil
			}
			// msg.Data is the correct size as per assertion in Write()
			copy(data[dataIdx:dataIdx+config.DataSize], msg.Data[:config.DataSize])
		}
		shards[i] = NewShard(bucketSize, data, pirBacking)
	}

	// Garbage collect old messages from s.messages
	// @todo

	s.msgLock.Unlock()
	return shards
}
