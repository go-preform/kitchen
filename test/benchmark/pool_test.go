package benchmark

import (
	"bytes"
	zmqGo "github.com/go-zeromq/zmq4"
	"sync"
	"testing"
)

func BenchmarkNewBytes(b *testing.B) {
	var (
		data     []byte
		toAppend = make([]byte, 512)
		header   = []byte{1}
	)
	for i := 0; i < b.N; i++ {
		data = make([]byte, 9)
		//binary.BigEndian.PutUint32(data[1:5], 1)
		//binary.BigEndian.PutUint32(data[5:9], 2)
		data = append(header, toAppend...)
		_ = data
	}
}

func BenchmarkBytesBuffer(b *testing.B) {
	var (
		buff     *bytes.Buffer
		toAppend = make([]byte, 512)
	)
	for i := 0; i < b.N; i++ {
		buff = bytes.NewBuffer(nil)
		//binary.Write(buff, binary.BigEndian, uint32(1))
		//binary.Write(buff, binary.BigEndian, uint32(2))
		buff.WriteByte(1)
		buff.Write(toAppend)

		_ = buff.Bytes()
	}
}

func BenchmarkBytesBufferPool(b *testing.B) {
	var (
		buffPool = sync.Pool{
			New: func() any {
				return bytes.NewBuffer(nil)
			},
		}
		buff     *bytes.Buffer
		toAppend = make([]byte, 512)
	)
	for i := 0; i < b.N; i++ {
		buff = buffPool.Get().(*bytes.Buffer)
		//binary.Write(buff, binary.BigEndian, uint32(1))
		//binary.Write(buff, binary.BigEndian, uint32(2))
		buff.WriteByte(1)
		buff.Write(toAppend)
		_ = buff.Bytes()
		buff.Truncate(1)
		buffPool.Put(buff)
	}
}

func BenchmarkMsgPool(b *testing.B) {
	var (
		pool = sync.Pool{
			New: func() any {
				return zmqGo.NewMsg(nil)
			},
		}
		data     zmqGo.Msg
		toAppend = []byte{1, 2, 3, 4}
	)
	for i := 0; i < b.N; i++ {
		data = pool.Get().(zmqGo.Msg)
		data.Frames[0] = toAppend
		_ = data
		pool.Put(data)
	}
}

func BenchmarkMsgNew(b *testing.B) {
	var (
		data     zmqGo.Msg
		toAppend = []byte{1, 2, 3, 4}
	)
	for i := 0; i < b.N; i++ {
		data = zmqGo.NewMsg(toAppend)
		_ = data
	}
}
