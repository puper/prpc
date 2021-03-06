package main

import (
	"net"

	"log"

	"time"

	"github.com/puper/prpc"
	"github.com/puper/prpc/example/proto"
)

type Front struct {
}

func (t *Front) Auth(args *proto.AuthArgs, reply *proto.AuthReply) error {
	if args.User == "puper" {
		reply.Success = true
	} else {
		reply.Success = false
	}
	return nil
}

func (t *Front) Mul(args *proto.ProtoArgs, reply *proto.ProtoReply) error {
	reply.C = args.A * args.B
	return nil
}

func main() {
	serviceManager := prpc.NewServiceManager()
	serviceManager.Register(new(Front))
	lis, err := net.Listen("tcp", ":8081")
	if err != nil {
		panic(err)
	}
	for {
		conn, err := lis.Accept()
		if err != nil {
			log.Println(err)
			continue
		}
		client := prpc.NewClient(conn)
		client.SetServer(serviceManager)
		go client.Loop()
		go func(client *prpc.Client) {
			for {
				req1 := new(proto.ProtoArgs)
				req1.A = 3
				req1.B = 3
				reply1 := new(proto.ProtoReply)
				err = client.Call("Front.Mul", req1, reply1)
				log.Println("error: ", err)
				log.Println("call result: ", reply1.C)
				time.Sleep(time.Second)
				if err != nil {
					return
				}
			}
		}(client)

	}
}
