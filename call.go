package prpc

import (
	"errors"
	"log"
	"sync"
)

var ErrShutdown = errors.New("connection is shut down")

type ServerError string

func (e ServerError) Error() string {
	return string(e)
}

type CallManager struct {
	codec Codec

	reqMutex sync.Mutex // protects following
	request  Header

	mutex    sync.Mutex // protects following
	seq      uint64
	pending  map[uint64]*Call
	closing  bool // user has called Close
	shutdown bool // server has told us to stop
}

func NewCallManager(codec Codec) *CallManager {
	callManager := &CallManager{
		codec:   codec,
		pending: make(map[uint64]*Call),
	}
	return callManager
}

type Call struct {
	ServiceMethod string      // The name of the service and method to call.
	Args          interface{} // The argument to the function (*struct).
	Reply         interface{} // The reply from the function (*struct).
	Error         error       // After completion, the error status.
	Done          chan *Call  // Strobes when call is complete.
	IsNotify      bool
}

func (call *Call) done() {
	select {
	case call.Done <- call:
	default:
	}
}

func (client *CallManager) send(call *Call) {
	client.reqMutex.Lock()
	defer client.reqMutex.Unlock()

	client.mutex.Lock()
	if client.shutdown || client.closing {
		client.mutex.Unlock()
		call.Error = ErrShutdown
		call.done()
		return
	}
	client.mutex.Unlock()
	if call.IsNotify {
		client.request.Seq = 0
		client.request.ServiceMethod = call.ServiceMethod
		err := client.codec.Write(&client.request, call.Args)
		if err != nil {
			call.Error = err
		}
		call.done()
	} else {
		client.mutex.Lock()
		client.seq++
		seq := client.seq
		client.pending[seq] = call
		client.mutex.Unlock()

		// Encode and send the request.
		client.request.Seq = seq
		client.request.ServiceMethod = call.ServiceMethod
		err := client.codec.Write(&client.request, call.Args)
		if err != nil {
			client.mutex.Lock()
			call = client.pending[seq]
			delete(client.pending, seq)
			client.mutex.Unlock()
			if call != nil {
				call.Error = err
				call.done()
			}
		}
	}
}

func (client *CallManager) _go(serviceMethod string, args interface{}, reply interface{}, done chan *Call, isNotify bool) *Call {
	call := new(Call)
	call.ServiceMethod = serviceMethod
	call.Args = args
	call.Reply = reply
	call.IsNotify = isNotify
	if done == nil {
		done = make(chan *Call, 10) // buffered.
	} else {
		if cap(done) == 0 {
			log.Panic("rpc: done channel is unbuffered")
		}
	}
	call.Done = done
	client.send(call)
	return call
}

func (client *CallManager) Go(serviceMethod string, args interface{}, reply interface{}, done chan *Call) *Call {
	return client._go(serviceMethod, args, reply, done, false)
}

func (client *CallManager) Call(serviceMethod string, args interface{}, reply interface{}) error {
	call := <-client.Go(serviceMethod, args, reply, make(chan *Call, 1)).Done
	return call.Error
}

func (client *CallManager) Notify(serviceMethod string, args interface{}, reply interface{}) error {
	call := <-client._go(serviceMethod, args, reply, make(chan *Call, 1), true).Done
	return call.Error
}
