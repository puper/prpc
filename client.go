package prpc

import (
	"errors"
	"io"
	"reflect"
	"strings"
	"sync"
)

type Client struct {
	CallManager
	server *ServiceManager
}

func (client *Client) SetServer(server *ServiceManager) {
	client.server = server
}

func NewClient(conn io.ReadWriteCloser) *Client {
	codec := NewProtobufCodec(conn)
	return NewClientWithCodec(codec)
}

func NewClientWithCodec(codec Codec) *Client {
	client := &Client{
		*NewCallManager(codec),
		NewServiceManager(),
	}
	return client
}

func (client *Client) readRequest(req *Header) (service *service, mtype *methodType, argv, replyv reflect.Value, keepReading bool, err error) {
	service, mtype, keepReading, err = client.readRequestHeader(req)
	if err != nil {
		if !keepReading {
			return
		}
		// discard body
		client.codec.ReadBody(nil)
		return
	}

	// Decode the argument value.
	argIsValue := false // if true, need to indirect before calling.
	if mtype.ArgType.Kind() == reflect.Ptr {
		argv = reflect.New(mtype.ArgType.Elem())
	} else {
		argv = reflect.New(mtype.ArgType)
		argIsValue = true
	}
	// argv guaranteed to be a pointer now.
	if err = client.codec.ReadBody(argv.Interface()); err != nil {
		return
	}
	if argIsValue {
		argv = argv.Elem()
	}

	replyv = reflect.New(mtype.ReplyType.Elem())
	return
}

func (client *Client) readRequestHeader(req *Header) (service *service, mtype *methodType, keepReading bool, err error) {
	keepReading = true
	dot := strings.LastIndex(req.ServiceMethod, ".")
	if dot < 0 {
		err = errors.New("rpc: service/method request ill-formed: " + req.ServiceMethod)
		return
	}
	serviceName := req.ServiceMethod[:dot]
	methodName := req.ServiceMethod[dot+1:]

	// Look up the request.
	client.server.mu.RLock()
	service = client.server.serviceMap[serviceName]
	client.server.mu.RUnlock()
	if service == nil {
		err = errors.New("rpc: can't find service " + req.ServiceMethod)
		return
	}
	mtype = service.method[methodName]
	if mtype == nil {
		err = errors.New("rpc: can't find method " + req.ServiceMethod)
	}
	return
}

func (client *Client) Loop() (err error) {
	var header *Header
	for err == nil {
		header = &Header{}
		err = client.codec.ReadHeader(header)
		if err != nil {
			break
		}
		if header.IsResp {
			seq := header.Seq
			client.mutex.Lock()
			call := client.pending[seq]
			delete(client.pending, seq)
			client.mutex.Unlock()
			switch {
			case call == nil:
				err = client.codec.ReadBody(nil)
				if err != nil {
					err = errors.New("reading error body: " + err.Error())
				}
			case header.Error != "":
				call.Error = ServerError(header.Error)
				err = client.codec.ReadBody(nil)
				if err != nil {
					err = errors.New("reading error body: " + err.Error())
				}
				call.done()
			default:
				err = client.codec.ReadBody(call.Reply)
				if err != nil {
					call.Error = errors.New("reading body " + err.Error())
				}
				call.done()
			}
		} else {
			sending := new(sync.Mutex)
			service, mtype, argv, replyv, keepReading, err := client.readRequest(header)
			if err != nil {
				if !keepReading {
					break
				}
				// send a response if we actually managed to read a header.
				if header != nil {
					client.server.sendResponse(sending, header, nil, client.codec, err.Error())
					client.server.freeRequest(header)
				}
				break
			}
			go service.call(client.server, sending, mtype, header, argv, replyv, client.codec)
		}
	}
	// Terminate pending calls.
	client.reqMutex.Lock()
	client.mutex.Lock()
	client.shutdown = true
	closing := client.closing
	if err == io.EOF {
		if closing {
			err = ErrShutdown
		} else {
			err = io.ErrUnexpectedEOF
		}
	}
	for _, call := range client.pending {
		call.Error = err
		call.done()
	}
	client.mutex.Unlock()
	client.reqMutex.Unlock()
	return
}
