package util

// Master
type Handle int64
type DFSPath string
type LinuxPath string
type Address string

// ChunkServer

// Client

// RPC structure
type CreateArg struct {
	Path DFSPath
}
type CreateRet struct {
}
type MkdirArg struct {
	Path DFSPath
}
type MkdirRet struct {
}
type DeleteArg struct {
	Path DFSPath
}
type DeleteRet struct {
}
type ListArg struct {
	Path DFSPath
}
type ListRet struct {
	Files []string
}
type GetReplicasArg struct {
	Path       DFSPath
	ChunkIndex int64
}
type GetReplicasRet struct {
	ChunkHandle      Handle
	ChunkServerAddrs []Address
}

const (
	MAXCHUNKSIZE = 64 << 20 // 64MB
)
