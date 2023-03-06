package kvraft

import (
	"log"
	"sync"
	"sync/atomic"
	"time"

	"6.824/labgob"
	"6.824/labrpc"
	"6.824/raft"
)

const (
	Debug               = false
	TimeoutInterval     = 500 * time.Millisecond
	TimeoutChanInterval = 3 * TimeoutInterval
)

func DPrintf(format string, a ...interface{}) (n int, err error) {
	if Debug {
		log.Printf(format, a...)
	}
	return
}

func (kv *KVServer) DPrintfKV() {
	if !Debug {
		return
	}
	kv.mu.Lock()
	defer kv.mu.Unlock()
	kvm := kv.kvMachine.(*MemoryKV)
	for k, v := range kvm.KV {
		DPrintf("[DBInfo ----]Key : %v, Value : %v", k, v)
	}
}

type KVServer struct {
	mu      sync.RWMutex
	me      int
	rf      *raft.Raft
	applyCh chan raft.ApplyMsg
	dead    int32 // set by Kill()

	maxraftstate int // snapshot if log grows this big

	// lastApplied int // record the lastApplied to prevent stateMachine from rollback
	kvMu      sync.RWMutex
	kvMachine KVStateMachine

	waitApplyCh    map[int]CommandChanStruct // index(raft) -> chan
	lastRequestId  map[int64]int             // clientid -> commandID
	lastOperations map[int64]LastOpStruct

	// last SnapShot point , raftIndex
	lastSSLogIndex int
}

func genOutdatedTime() time.Time {
	return time.Now().Add(TimeoutChanInterval)
}

// create waitForCh
func (kv *KVServer) UseOrCreateWaitChan(ind int) chan CommandReply {
	chanForRaftInd, ok := kv.waitApplyCh[ind]
	if !ok {
		kv.waitApplyCh[ind] = CommandChanStruct{
			ChanReply: make(chan CommandReply, 1),
			Outdated:  genOutdatedTime(),
		}
		chanForRaftInd = kv.waitApplyCh[ind]
	} else {
		chanForRaftInd.Outdated = genOutdatedTime()
	}
	return chanForRaftInd.ChanReply
}

func (kv *KVServer) RemoveWaitChan(ind int) {
	for {
		time.Sleep(TimeoutChanInterval)

		kv.mu.Lock()
		outdated := kv.waitApplyCh[ind].Outdated
		if time.Now().After(outdated) {
			delete(kv.waitApplyCh, ind)
			kv.mu.Unlock()
			return
		}
		kv.mu.Unlock()
	}
}

// rlock_guard
func (kv *KVServer) isRequestDuplicate(clientId int64, commandId int) bool {
	lastOp, ok := kv.lastOperations[clientId]
	if !ok {
		return false
	}
	return lastOp.CommandId == commandId
}

func (kv *KVServer) Command(args *CommandArgs, reply *CommandReply) {
	defer DPrintf("{Node %v} processes CommandRequest %v with CommandResponse %v",
		kv.rf.Me(), args, reply)
	// return result directly without raft layer's participation if request is duplicated

	kv.mu.RLock()
	if args.Op != GetOp && kv.isRequestDuplicate(args.ClientId, args.CommandId) {
		lastReply := kv.lastOperations[args.ClientId].LastReply
		reply.Value, reply.Err = lastReply.Value, lastReply.Err
		kv.mu.RUnlock()
		return
	}
	kv.mu.RUnlock()

	ind, _, isLeader := kv.rf.Start(args)
	if isLeader {
		reply.Err = ErrWrongLeader
		return
	}

	kv.mu.Lock()
	ch := kv.UseOrCreateWaitChan(ind)
	kv.mu.Unlock()

	select {
	case result := <-ch:
		// ind is the only one.
		reply.Value, reply.Err = result.Value, result.Err
	case <-time.After(TimeoutInterval):
		reply.Err = ErrTimeout
	}

	go func() {
		kv.mu.Lock()
		kv.RemoveWaitChan(ind)
		kv.mu.Unlock()
	}()
}

//
// the tester calls Kill() when a KVServer instance won't
// be needed again. for your convenience, we supply
// code to set rf.dead (without needing a lock),
// and a killed() method to test rf.dead in
// long-running loops. you can also add your own
// code to Kill(). you're not required to do anything
// about this, but it may be convenient (for example)
// to suppress debug output from a Kill()ed instance.
//
func (kv *KVServer) Kill() {
	atomic.StoreInt32(&kv.dead, 1)
	kv.rf.Kill()
}

func (kv *KVServer) killed() bool {
	z := atomic.LoadInt32(&kv.dead)
	return z == 1
}

//
// servers[] contains the ports of the set of
// servers that will cooperate via Raft to
// form the fault-tolerant key/value service.
// me is the index of the current server in servers[].
// the k/v server should store snapshots through the underlying Raft
// implementation, which should call persister.SaveStateAndSnapshot() to
// atomically save the Raft state along with the snapshot.
// the k/v server should snapshot when Raft's saved state exceeds maxraftstate bytes,
// in order to allow Raft to garbage-collect its log. if maxraftstate is -1,
// you don't need to snapshot.
// StartKVServer() must return quickly, so it should start goroutines
// for any long-running work.
//
func StartKVServer(servers []*labrpc.ClientEnd, me int, persister *raft.Persister, maxraftstate int) *KVServer {
	DPrintf("[InitKVServer---]Server %d", me)
	// call labgob.Register on structures you want
	// Go's RPC library to marshall/unmarshall.
	labgob.Register(CommandArgs{})

	kv := new(KVServer)
	kv.me = me
	kv.maxraftstate = maxraftstate

	kv.applyCh = make(chan raft.ApplyMsg)
	kv.rf = raft.Make(servers, me, persister, kv.applyCh)

	kv.kvMachine = NewMemoryKV()
	kv.waitApplyCh = make(map[int]CommandChanStruct)
	kv.lastRequestId = make(map[int64]int)
	kv.lastOperations = make(map[int64]LastOpStruct)

	snapshot := persister.ReadSnapshot()
	if len(snapshot) > 0 {
		kv.ReadSnapshot(snapshot)
	}

	go kv.applier()
	return kv
}
