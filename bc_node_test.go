package abciproxy

import (
	"bytes"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"syscall"
	"time"

	abcicli "github.com/tendermint/abci/client"
	"github.com/tendermint/abci/server"
	cmn "github.com/tendermint/tmlibs/common"
	tmlog "github.com/tendermint/tmlibs/log"
)

type BCNodeID int

// A BCNode consist of a tendermint node, its application and the
// proxy running between those This object is here to maintain sane
// configuration and overall start/stop function for testing purpose
// on a single computer
type BCNode struct {
	ID BCNodeID

	//tendermint node management fields
	wDir         string
	tmNodeCmd    *exec.Cmd
	tmNodeOutput *bytes.Buffer

	//proxy app management
	proxy        *ProxyApplication
	proxyService cmn.Service
	appClient    abcicli.Client
	proxyOutput  *bytes.Buffer
	// target app management

	testApplication *TestApplication
	app             cmn.Service
	appOutput       *bytes.Buffer
}

const MaxBCNodeID BCNodeID = 99

const ProxyAppPortStart int = 50000
const RPCPortStart int = 50100
const P2PPortStart int = 50200
const AppPortStart int = 50300
const RPCProxyStart int = 50400

// NewBCNode instantiate a new node for testing ID should be unique
// between 0 and 99, rootDir is the path to a directory where all
// config data will be stored.
func NewBCNode(ID BCNodeID, rootDir string) (*BCNode, error) {
	if ID > MaxBCNodeID {
		return nil, fmt.Errorf("Maximum  BCNodeID for test is %d, got %d", MaxBCNodeID, ID)
	}

	res := &BCNode{
		ID:              ID,
		wDir:            filepath.Join(rootDir, fmt.Sprintf("BCNode%02d", ID)),
		tmNodeOutput:    bytes.NewBuffer(make([]byte, 0, 4096)),
		proxyOutput:     bytes.NewBuffer(make([]byte, 0, 4096)),
		appOutput:       bytes.NewBuffer(make([]byte, 0, 4096)),
		testApplication: NewTestApplication(false),
	}

	err := os.MkdirAll(res.wDir, 0755)
	if err != nil {
		return nil, err
	}

	initCmd := exec.Command("tendermint", "init", "--home", res.wDir)
	output, err := initCmd.CombinedOutput()
	if err != nil {
		return nil, fmt.Errorf("Could not initialize tendermint: %s\n---output---\n%s", err, output)
	}

	return res, nil
}

// Start starts the node ;)

func (n *BCNode) Start(peers []*BCNode) error {
	var err error
	//start app
	n.app, err = server.NewServer(fmt.Sprintf("tcp://127.0.0.1:%d", n.AppPort()),
		"socket",
		n.testApplication)
	if err != nil {
		return err
	}
	n.app.SetLogger(tmlog.NewTMLogger(tmlog.NewSyncWriter(n.appOutput)).With("module", "abci-server"))
	if _, err := n.app.Start(); err != nil {
		return err
	}
	//let time to the app to start
	time.Sleep(20 * time.Millisecond)

	//start proxy
	n.appClient = abcicli.NewSocketClient(fmt.Sprintf("tcp://127.0.0.1:%d", n.AppPort()), true)
	if _, err := n.appClient.Start(); err != nil {
		return err
	}

	n.proxy = NewProxyApp(n.appClient)
	n.proxyService, err = server.NewServer(fmt.Sprintf("tcp://127.0.0.1:%d", n.ProxyAppPort()),
		"socket",
		n.proxy)
	if err != nil {
		return err
	}

	n.proxyService.SetLogger(tmlog.NewTMLogger(tmlog.NewSyncWriter(n.proxyOutput)).With("module", "abci-server"))
	if _, err := n.proxyService.Start(); err != nil {
		return err
	}

	//start tendermint node
	n.tmNodeCmd = exec.Command("tendermint", "node",
		"--home", n.wDir,
		"--proxy_app", fmt.Sprintf("tcp://127.0.0.1:%d", n.ProxyAppPort()),
		"--p2p.laddr", fmt.Sprintf("tcp://127.0.0.1:%d", n.P2PPort()),
		"--rpc.laddr", fmt.Sprintf("tcp://127.0.0.1:%d", n.RPCPort()),
		"--p2p.seeds="+n.FormatPeerListOptions(peers))
	//save the output in the buffer
	n.tmNodeCmd.Stdin = nil
	n.tmNodeCmd.Stdout = n.tmNodeOutput
	n.tmNodeCmd.Stderr = n.tmNodeOutput

	err = n.tmNodeCmd.Start()
	if err != nil {
		return err
	}

	return nil
}

func (n *BCNode) FormatPeerListOptions(peers []*BCNode) string {
	res := ""
	for _, p := range peers {
		if p.ID == n.ID {
			continue
		}
		if len(res) > 0 {
			res += ","
		}
		res += fmt.Sprintf("127.0.0.1:%d", p.P2PPort())
	}
	return res
}

// Stop stops the node
func (n *BCNode) Stop() error {
	//stop tendermint
	err := n.tmNodeCmd.Process.Signal(syscall.SIGINT)
	if err != nil {
		return fmt.Errorf("Could not interrupt tendermint node [%d]: %s\n---output---\n%s", n.ID, err, n.tmNodeOutput.String())
	}

	err = n.tmNodeCmd.Wait()
	if err != nil && err.Error() != "signal: interrupt" && err.Error() != "exit status 1" {
		return fmt.Errorf("Could not wait on interupt of tendermint node [%d] : %s\n---output---\n%s", n.ID, err, n.tmNodeOutput.String())
	}

	//stop proxy
	if ok := n.proxyService.Stop(); ok == false {
		return fmt.Errorf("Could not stop proxy service %d", n.ID)
	}
	if ok := n.appClient.Stop(); ok == false {
		return fmt.Errorf("Could not close target app connection %d", n.ID)
	}
	//stop app
	if ok := n.app.Stop(); ok == false {
		return fmt.Errorf("Could not stop target app %d", n.ID)
	}

	return nil
}

// these method just define some convention on the port these nodes uses
func (n *BCNode) ProxyAppPort() int {
	return ProxyAppPortStart + int(n.ID)
}

func (n *BCNode) RPCPort() int {
	return RPCPortStart + int(n.ID)
}

func (n *BCNode) P2PPort() int {
	return P2PPortStart + int(n.ID)
}

func (n *BCNode) AppPort() int {
	return AppPortStart + int(n.ID)
}

func (n *BCNode) RPCProxyPort() int {
	return RPCProxyStart + int(n.ID)
}

func (n *BCNode) WorkingDir() string {
	return n.wDir
}
