package state

import (
	"context"
	"sync"
	"time"

	"github.com/golang/protobuf/ptypes"
	"github.com/sirupsen/logrus"
	"google.golang.org/grpc"

	"github.com/go-ignite/ignite-agent/protos"
	"github.com/go-ignite/ignite/config"
	"github.com/go-ignite/ignite/model"
)

type Node struct {
	lock      sync.RWMutex
	config    *config.State
	node      *model.Node
	services  map[string]*Service
	ports     map[int]bool
	available bool
	conn      *grpc.ClientConn
	client    protos.AgentServiceClient
	done      chan struct{}
}

func newNode(node *model.Node, services []*model.Service) (*Node, error) {
	conn, err := grpc.Dial(node.RequestAddress, grpc.WithInsecure())
	if err != nil {
		return nil, err
	}

	n := &Node{
		node:     node,
		services: map[string]*Service{},
		ports:    map[int]bool{},
		conn:     conn,
		client:   protos.NewAgentServiceClient(conn),
		done:     make(chan struct{}),
	}
	for _, s := range services {
		n.services[s.UserID] = newService(s)
		n.ports[s.Port] = true
	}

	return n, nil
}

func (n *Node) setAvailable(available bool) {
	n.lock.Lock()
	defer n.lock.Unlock()

	n.available = available
}

func (n *Node) applySyncResponse(resp *protos.SyncStreamServer) {
	n.lock.RLock()
	defer n.lock.RUnlock()

	for _, s := range resp.Services {
		if service, ok := n.services[s.ServiceId]; ok {
			service.updateSyncResponse(s)
		}
	}
}

func (n *Node) sync() {
	ctx, cancel := context.WithCancel(context.Background())
	go func() {
		<-n.done
		cancel()
	}()

	wg := sync.WaitGroup{}
	wg.Add(2)

	go func() {
		defer wg.Done()
		for {
			if err := func() error {
				req := &protos.HeartbeatRequest{
					Interval: ptypes.DurationProto(n.config.HeartbeatInterval),
				}
				stream, err := n.client.Heartbeat(ctx, req)
				if err != nil {
					return err
				}

				for {
					_, err := stream.Recv()
					n.setAvailable(err == nil)
					if err != nil {
						return err
					}
				}
			}(); err != nil {
				if err == context.Canceled {
					return
				}

				logrus.WithError(err).WithField("nodeID", n.node.ID).Error("state: node heartbeat error")
			}

			select {
			case <-time.After(n.config.StreamRetryInterval):
			case <-ctx.Done():
				return
			}
		}
	}()

	go func() {
		defer wg.Done()
		for {
			if err := func() error {
				req := &protos.SyncRequest{
					SyncInterval: ptypes.DurationProto(n.config.SyncInterval),
				}
				stream, err := n.client.Sync(ctx, req)
				if err != nil {
					return err
				}

				for {
					resp, err := stream.Recv()
					if err != nil {
						return err
					}

					n.applySyncResponse(resp)
				}
			}(); err != nil {
				if err == context.Canceled {
					return
				}

				logrus.WithError(err).WithField("nodeID", n.node.ID).Error("state: node sync error")
			}

			select {
			case <-time.After(n.config.StreamRetryInterval):
			case <-ctx.Done():
				return
			}
		}
	}()

	wg.Wait()
	close(n.done)
}

func (n *Node) stopSync() {
	n.done <- struct{}{}
	<-n.done

	_ = n.conn.Close()
}
