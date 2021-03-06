package network_test

import (
	"fmt"
	"github.com/LuisZhou/lpge/log"
	"github.com/LuisZhou/lpge/network"
	"sync"
	"testing"
	_ "time"
)

var wg sync.WaitGroup

type TestAgent struct {
	conn *network.WSConn
	name string
}

func (a *TestAgent) Run() {
	fmt.Println("run")

	if a.name == "client_agent" {
		a.conn.WriteMsg(1, []byte{1, 2})
	}

	for {
		if a.name == "server_agent" {
			cmd, data, err := a.conn.ReadMsg()
			if err != nil {
				fmt.Println("server read message: %v", err) // conn will close.
				break
			}
			fmt.Println("server read message: %v", cmd, data)
			a.conn.WriteMsg(2, []byte{3, 4})
		}

		if a.name == "client_agent" {
			cmd, data, err := a.conn.ReadMsg()
			if err != nil {
				fmt.Println("client read message: %v", err) // conn will close.
				break
			}
			fmt.Println("client read message: %v", cmd, data)
			wg.Done()

			a.conn.Close()
		}
	}
}

func (a *TestAgent) OnClose() {
	log.Debug("agent close")
	wg.Done()
}

func TestNewWsServer(t *testing.T) {
	wg.Add(3)

	tcpServer := new(network.WSServer)
	tcpServer.Addr = "0.0.0.0:6001"
	tcpServer.MaxConnNum = 100
	tcpServer.PendingWriteNum = 100
	tcpServer.MaxMsgLen = 0
	tcpServer.NewAgent = func(conn *network.WSConn) network.Agent {
		fmt.Println("new client")
		a := &TestAgent{conn: conn}
		a.name = "server_agent"
		return a
	}
	tcpServer.Start()

	wsClient := &network.WSClient{
		Addr: "ws://localhost:6001",
		NewAgent: func(conn *network.WSConn) network.Agent {
			fmt.Println("new client")
			a := &TestAgent{conn: conn}
			a.name = "client_agent"
			return a
		},
	}
	wsClient.Start()

	wg.Wait()

	//time.Sleep(1 * time.Second)
}
