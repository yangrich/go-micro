package server

import (
	"bytes"
	"sync"

	c "github.com/myodc/go-micro/context"
	"github.com/myodc/go-micro/transport"

	log "github.com/golang/glog"
	rpc "github.com/youtube/vitess/go/rpcplus"
	js "github.com/youtube/vitess/go/rpcplus/jsonrpc"
	pb "github.com/youtube/vitess/go/rpcplus/pbrpc"

	"golang.org/x/net/context"
)

type RpcServer struct {
	mtx     sync.RWMutex
	address string
	opts    options
	rpc     *rpc.Server
	exit    chan chan error
}

var (
	HealthPath = "/_status/health"
	RpcPath    = "/_rpc"
)

func (s *RpcServer) accept(sock transport.Socket) {
	var msg transport.Message
	if err := sock.Recv(&msg); err != nil {
		return
	}

	rbq := bytes.NewBuffer(msg.Body)
	rsp := bytes.NewBuffer(nil)
	defer rsp.Reset()
	defer rbq.Reset()

	buf := &buffer{
		rbq,
		rsp,
	}

	var cc rpc.ServerCodec
	switch msg.Header["Content-Type"] {
	case "application/octet-stream":
		cc = pb.NewServerCodec(buf)
	case "application/json":
		cc = js.NewServerCodec(buf)
	default:
		return
	}

	// strip our headers
	ct := msg.Header["Content-Type"]
	delete(msg.Header, "Content-Type")

	ctx := c.WithMetaData(context.Background(), msg.Header)

	if err := s.rpc.ServeRequestWithContext(ctx, cc); err != nil {
		return
	}

	sock.Send(&transport.Message{
		Header: map[string]string{
			"Content-Type": ct,
		},
		Body: rsp.Bytes(),
	})
}

func (s *RpcServer) Address() string {
	s.mtx.RLock()
	address := s.address
	s.mtx.RUnlock()
	return address
}

func (s *RpcServer) Init() error {
	return nil
}

func (s *RpcServer) NewReceiver(handler interface{}) Receiver {
	return newRpcReceiver("", handler)
}

func (s *RpcServer) NewNamedReceiver(name string, handler interface{}) Receiver {
	return newRpcReceiver(name, handler)
}

func (s *RpcServer) Register(r Receiver) error {
	if len(r.Name()) > 0 {
		s.rpc.RegisterName(r.Name(), r.Handler())
		return nil
	}

	s.rpc.Register(r.Handler())
	return nil
}

func (s *RpcServer) Start() error {
	registerHealthChecker(s)

	ts, err := s.opts.transport.Listen(s.address)
	if err != nil {
		return err
	}

	log.Infof("Listening on %s", ts.Addr())

	s.mtx.RLock()
	s.address = ts.Addr()
	s.mtx.RUnlock()

	go ts.Accept(s.accept)

	go func() {
		ch := <-s.exit
		ch <- ts.Close()
	}()

	return nil
}

func (s *RpcServer) Stop() error {
	ch := make(chan error)
	s.exit <- ch
	return <-ch
}

func NewRpcServer(address string, opt ...Option) *RpcServer {
	var opts options

	for _, o := range opt {
		o(&opts)
	}

	if opts.transport == nil {
		opts.transport = transport.DefaultTransport
	}

	return &RpcServer{
		opts:    opts,
		address: address,
		rpc:     rpc.NewServer(),
		exit:    make(chan chan error),
	}
}
