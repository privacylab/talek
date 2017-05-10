package pirtest

import (
	"math/rand"
	"testing"

	"github.com/privacylab/talek/pir"
)

type FatalInterface interface {
	Fatal(args ...interface{})
	Fatalf(format string, args ...interface{})
}

const (
	TestBatchSize    = 3
	TestNumMessages  = 32
	TestMessageSize  = 8
	TestDepth        = 2       // 16 buckets
	BenchBatchSize   = 128     // This seems to provide the best GPU perf
	BenchNumMessages = 1048576 // 2^20
	//BenchNumMessages = 524288 // 2^19; Note: AMD devices have a smaller max memory allocation size
	BenchMessageSize = 1024
	BenchDepth       = 4 // 262144=2^18 buckets
)

func AfterEach(f FatalInterface, shard pir.Shard, context pir.Context) {
	var err error
	if shard != nil {
		err = shard.Free()
		if err != nil {
			f.Fatalf("error freeing shard: %v\n", err)
		}
	}
	if context != nil {
		err = context.Free()
		if err != nil {
			f.Fatalf("error freeing context: %v\n", err)
		}
	}
}

func GenerateData(size int) []byte {
	data := make([]byte, size)
	for i := 0; i < size; i++ {
		data[i] = byte(i)
	}
	return data
}

func HelperTestShardRead(t *testing.T, shard pir.Shard) {

	// Populate batch read request
	reqLength := shard.GetNumBuckets() / 8
	if shard.GetNumBuckets()%8 != 0 {
		reqLength++
	}
	reqs := make([]byte, reqLength*TestBatchSize)
	setBit := func(reqs []byte, reqIndex int, bucketIndex int) {
		reqs[reqIndex*reqLength+(bucketIndex/8)] |= byte(1) << uint(bucketIndex%8)
	}
	setBit(reqs, 0, 1)
	setBit(reqs, 1, 0)
	setBit(reqs, 2, 0)
	setBit(reqs, 2, 1)
	setBit(reqs, 2, 2)

	if shard.GetNumBuckets() < 3 {
		t.Fatalf("test misconfigured. shard has %d buckets, needs %d\n", shard.GetNumBuckets(), 3)
	}

	// Batch Read
	response, err := shard.Read(reqs, reqLength)
	//fmt.Printf("%v\n", response)

	// Check fail
	if err != nil {
		t.Fatalf("error calling shard.Read: %v\n", err)
	}

	if response == nil {
		t.Fatalf("no response received")
	}

	bucketSize := shard.GetBucketSize()
	data := shard.GetData()
	// Check request 0
	res := response[0:bucketSize]
	for i := 0; i < bucketSize; i++ {
		if res[i] != data[bucketSize+i] {
			t.Fatalf("response0 is incorrect. byte %d was %d, not '%d'\n", i, res[i], bucketSize+i)
		}
	}
	// Check request 1
	res = response[bucketSize : 2*bucketSize]
	for i := 0; i < bucketSize; i++ {
		if res[i] != data[i] {
			t.Fatalf("response1 is incorrect. byte %d was %d, not '%d'\n", i, res[i], i)
		}
	}
	// Check request 2
	res = response[2*bucketSize : 3*bucketSize]
	for i := 0; i < bucketSize; i++ {
		expected := data[i] ^ data[bucketSize+i] ^ data[2*bucketSize+i]
		if res[i] != expected {
			t.Fatalf("response2 is incorrect. byte %d was %d, not '%d'\n", i, res[i], expected)
		}
	}
}

func HelperBenchmarkShardRead(b *testing.B, shard pir.Shard, batchSize int) {
	reqLength := shard.GetNumBuckets() / 8
	if shard.GetNumBuckets()%8 != 0 {
		reqLength++
	}
	reqs := make([]byte, reqLength*batchSize)
	for i := 0; i < len(reqs); i++ {
		reqs[i] = byte(rand.Int())
	}

	// Start test
	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		_, err := shard.Read(reqs, reqLength)

		if err != nil {
			b.Fatalf("Read error: %v\n", err)
		}
	}
	b.StopTimer()
}