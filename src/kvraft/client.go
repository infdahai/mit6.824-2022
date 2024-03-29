package kvraft

import (
	"crypto/rand"
	"math/big"
	"time"

	"6.824/labrpc"

	mathrand "math/rand"
)

type Clerk struct {
	servers   []*labrpc.ClientEnd
	clientId  int64
	commandId int
	leaderId  int
}

func nrand() int64 {
	max := big.NewInt(int64(1) << 62)
	bigx, _ := rand.Int(rand.Reader, max)
	x := bigx.Int64()
	return x
}

func NServerRand(len int) int {
	return mathrand.Intn(len)
}

func MakeClerk(servers []*labrpc.ClientEnd) *Clerk {
	ck := new(Clerk)
	ck.servers = servers
	ck.clientId = nrand()
	ck.leaderId = NServerRand(len(servers))
	return ck
}

//
// fetch the current value for a key.
// returns "" if the key does not exist.
// keeps trying forever in the face of all other errors.
//
// you can send an RPC with code like this:
// ok := ck.servers[i].Call("KVServer.Get", &args, &reply)
//
// the types of args and reply (including whether they are pointers)
// must match the declared types of the RPC handler function's
// arguments. and reply must be passed as a pointer.
//
func (ck *Clerk) args() Args {
	return Args{ClientId: ck.clientId, CommandId: ck.commandId}
}

func (ck *Clerk) nextLeader(leaderId int) int {
	return (leaderId + 1) % len(ck.servers)
}

const RetryInterval = 80 * time.Millisecond

func (ck *Clerk) Command(req *CommandArgs) string {
	req.Args = ck.args()
	server := ck.leaderId

	for {
		var reply CommandReply
		ok := ck.servers[server].Call("KVServer.Command", req, &reply)

		if !ok || reply.Err == ErrTimeout {
			server = ck.nextLeader(server)
			continue
		}
		if reply.Err == ErrWrongLeader {
			// time.Sleep(RetryInterval)
			server = ck.nextLeader(server)
			continue
		}
		ck.commandId++
		ck.leaderId = server
		return reply.Value
	}
}

func (ck *Clerk) Get(key string) string {
	return ck.Command(&CommandArgs{Key: key, Op: GetOp})
}

//
// shared by Put and Append.
//
// you can send an RPC with code like this:
// ok := ck.servers[i].Call("KVServer.PutAppend", &args, &reply)
//
// the types of args and reply (including whether they are pointers)
// must match the declared types of the RPC handler function's
// arguments. and reply must be passed as a pointer.
//

func (ck *Clerk) Put(key string, value string) {
	ck.Command(&CommandArgs{Key: key, Value: value, Op: PutOp})
}
func (ck *Clerk) Append(key string, value string) {
	ck.Command(&CommandArgs{Key: key, Value: value, Op: AppendOp})
}
