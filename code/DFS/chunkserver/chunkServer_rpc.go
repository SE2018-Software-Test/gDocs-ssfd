package chunkserver

import (
	"DFS/util"
	"fmt"
	"log"
	"net"
	"net/rpc"
	"os"
)

func (cs *ChunkServer) StartRPCServer() error {
	rpcs := rpc.NewServer()
	rpcs.Register(cs)
	listener, err := net.Listen("tcp", string(cs.addr))
	if err != nil {
		log.Fatalf("ChunKserver listen %v error\n", string(cs.addr))
	}

	cs.l = listener

	go func() {
	loop:
		for {
			select {
			case <-cs.shutdown:
				break loop
			default:
			}
			conn, err := cs.l.Accept()
			if err != nil {
				log.Fatal("chunkserver accept error\n")
			} else {
				go func() {
					rpcs.ServeConn(conn)
					conn.Close()
				}()
			}
		}
	}()

	return err
}

func (cs *ChunkServer) LoadDataRPC(args util.LoadDataArgs, reply *util.LoadDataReply) error {
	cs.cache.Set(args.Handle, args.Data)
	if len(args.Addrs) > 0 {
		newArgs := util.LoadDataArgs{
			Data:   args.Data,
			Handle: args.Handle,
			Addrs:  args.Addrs[1:],
		}
		err := util.Call(string(args.Addrs[0]), "ChunkServer.LoadDataRPC", newArgs, reply)
		return err
	}
	return nil
}

func (cs *ChunkServer) ReadChunkRPC(args util.ReadChunkArgs, reply *util.ReadChunkReply) error {
	buf := make([]byte, args.Len)
	len, err := cs.GetChunk(args.Handle, args.Off, buf)
	if err != nil {
		log.Fatalf("get chunk error\n")
		return err
	}

	reply.Buf = buf[:len]
	reply.Len = len

	if args.Len > len {
		return fmt.Errorf("the length in args is larger than chunk len\n")
	}

	return nil
}

func (cs *ChunkServer) CreateChunkRPC(args util.CreateChunkArgs, reply *util.CreateChunkReply) error {
	filename := cs.GetFileName(args.Handle)
	fd, err := os.OpenFile(filename, os.O_WRONLY|os.O_CREATE, 0644)
	defer fd.Close()
	return err
}

//call by client
// func (cs *ChunkServer) SyncRPC(args util.SyncArgs, reply *util.SyncReply) error {

// }