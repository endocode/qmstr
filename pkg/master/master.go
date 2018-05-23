package master

import (
	"bufio"
	"bytes"
	"errors"
	"fmt"
	"log"
	"math/rand"
	"net"
	"strings"
	"sync"
	"sync/atomic"

	"golang.org/x/net/context"

	"github.com/QMSTR/qmstr/pkg/config"
	"github.com/QMSTR/qmstr/pkg/database"
	"github.com/QMSTR/qmstr/pkg/service"
	"google.golang.org/grpc"
)

var quitServer chan interface{}
var phaseMap map[int32]serverPhase

type serverPhase interface {
	GetPhaseId() int32
	Activate() error
	Shutdown() error
	Build(*service.BuildMessage) (*service.BuildResponse, error)
	GetAnalyzerConfig(*service.AnalyzerConfigRequest) (*service.AnalyzerConfigResponse, error)
	GetReporterConfig(*service.ReporterConfigRequest) (*service.ReporterConfigResponse, error)
	GetNodes(*service.NodeRequest) (*service.NodeResponse, error)
	SendNodes(*service.AnalysisMessage) (*service.AnalysisResponse, error)
}

type genericServerPhase struct {
	Name         string
	phaseId      int32
	db           *database.DataBase
	session      string
	serverConfig *config.ServerConfig
}

type server struct {
	db                 *database.DataBase
	analysisClosed     chan bool
	serverMutex        *sync.Mutex
	analysisDone       bool
	currentPhase       serverPhase
	pendingPhaseSwitch int64
}

func (s *server) Build(ctx context.Context, in *service.BuildMessage) (*service.BuildResponse, error) {
	return s.currentPhase.Build(in)
}

func (s *server) GetAnalyzerConfig(ctx context.Context, in *service.AnalyzerConfigRequest) (*service.AnalyzerConfigResponse, error) {
	return s.currentPhase.GetAnalyzerConfig(in)
}

func (s *server) GetReporterConfig(ctx context.Context, in *service.ReporterConfigRequest) (*service.ReporterConfigResponse, error) {
	return s.currentPhase.GetReporterConfig(in)
}

func (s *server) GetNodes(ctx context.Context, in *service.NodeRequest) (*service.NodeResponse, error) {
	return s.currentPhase.GetNodes(in)
}

func (s *server) SendNodes(ctx context.Context, in *service.AnalysisMessage) (*service.AnalysisResponse, error) {
	return s.currentPhase.SendNodes(in)
}

func (s *server) GetPackageNode(ctx context.Context, in *service.PackageRequest) (*service.PackageResponse, error) {
	node, err := s.db.GetPackageNode(in.Session)
	if err != nil {
		return nil, err
	}
	return &service.PackageResponse{PackageNode: node}, nil
}

func (s *server) SwitchPhase(ctx context.Context, in *service.SwitchPhaseMessage) (*service.SwitchPhaseResponse, error) {
	if !atomic.CompareAndSwapInt64(&s.pendingPhaseSwitch, 0, 1) {
		errMsg := "denied there is a pending phase transition"
		log.Println(errMsg)
		return &service.SwitchPhaseResponse{Success: false}, errors.New(errMsg)
	}
	requestedPhase := in.Phase
	if requestedPhase <= s.currentPhase.GetPhaseId() {
		errMsg := fmt.Sprintf("Illegal phase transition %d->%d requested", s.currentPhase.GetPhaseId(), requestedPhase)
		log.Println(errMsg)
		return &service.SwitchPhaseResponse{Success: false}, errors.New(errMsg)
	}
	if phase, ok := phaseMap[requestedPhase]; ok {
		log.Printf("Switching to phase %d", requestedPhase)
		s.currentPhase.Shutdown()
		s.currentPhase = phase
		s.pendingPhaseSwitch = 0
		err := s.currentPhase.Activate()
		if err != nil {
			return &service.SwitchPhaseResponse{Success: false}, err
		}
		return &service.SwitchPhaseResponse{Success: true}, nil
	}
	return &service.SwitchPhaseResponse{Success: false}, fmt.Errorf("Invalid phase requested %d", requestedPhase)
}

func (s *server) Log(ctx context.Context, in *service.LogMessage) (*service.LogResponse, error) {
	log.Printf("REMOTE: %s", string(in.Msg))
	return &service.LogResponse{Success: true}, nil
}

func (s *server) Quit(ctx context.Context, in *service.QuitMessage) (*service.QuitResponse, error) {
	if in.Kill {
		log.Fatalf("qmstr was killed hard by client")
	}

	// Wait for pending tasks to complete e.g. synchronize channels

	// Schedule shutdown
	quitServer <- nil

	return &service.QuitResponse{Success: true}, nil
}

func InitAndRun(configfile string) error {
	masterConfig, err := config.ReadConfigFromFile(configfile)
	if err != nil {
		return err
	}

	// Connect to backend database (dgraph)
	db, err := database.Setup(masterConfig.Server.DBAddress, masterConfig.Server.DBWorkers)
	if err != nil {
		return fmt.Errorf("Could not setup database: %v", err)
	}

	// Setup buildservice
	lis, err := net.Listen("tcp", masterConfig.Server.RPCAddress)
	if err != nil {
		return fmt.Errorf("Failed to setup socket and listen: %v", err)
	}

	sessionBytes := make([]byte, 32)
	rand.Read(sessionBytes)
	session := fmt.Sprintf("%x", sessionBytes)

	phaseMap = map[int32]serverPhase{
		1: &serverPhaseBuild{genericServerPhase{Name: "Build phase", phaseId: 1, db: db, session: session, serverConfig: masterConfig.Server}},
		2: newAnalysisPhase(genericServerPhase{Name: "Analysis phase", phaseId: 2, db: db, serverConfig: masterConfig.Server, session: session},
			masterConfig.Analysis),
		3: &serverPhaseReport{genericServerPhase{Name: "Reporting phase", phaseId: 3, db: db, serverConfig: masterConfig.Server, session: session}, masterConfig.Reporting},
	}

	s := grpc.NewServer()
	serverImpl := &server{
		db:             db,
		serverMutex:    &sync.Mutex{},
		analysisClosed: make(chan bool),
		analysisDone:   false,
		currentPhase:   phaseMap[1],
	}
	service.RegisterBuildServiceServer(s, serverImpl)
	service.RegisterAnalysisServiceServer(s, serverImpl)
	service.RegisterReportServiceServer(s, serverImpl)
	service.RegisterControlServiceServer(s, serverImpl)

	quitServer = make(chan interface{})
	go func() {
		<-quitServer
		log.Println("qmstr-master terminated by client")
		s.GracefulStop()
		close(quitServer)
		quitServer = nil
	}()

	initPackage(masterConfig, db, session)

	log.Printf("qmstr-master listening on %s\n", masterConfig.Server.RPCAddress)
	if err := s.Serve(lis); err != nil {
		return fmt.Errorf("Failed to start rpc service %v", err)
	}
	return nil
}

func initPackage(masterConfig *config.MasterConfig, db *database.DataBase, session string) {
	rootPackageNode := &service.PackageNode{Name: masterConfig.Name}
	tmpInfoNode := &service.InfoNode{Type: "metadata", NodeType: service.NodeTypeInfoNode}
	for key, val := range masterConfig.MetaData {
		tmpInfoNode.DataNodes = append(tmpInfoNode.DataNodes, &service.InfoNode_DataNode{Type: key, Data: val, NodeType: service.NodeTypeDataNode})
	}

	if len(tmpInfoNode.DataNodes) > 0 {
		rootPackageNode.AdditionalInfo = []*service.InfoNode{tmpInfoNode}
	}

	rootPackageNode.Session = session
	db.AddPackageNode(rootPackageNode)
}

func logModuleError(moduleName string, output []byte) {
	var buffer bytes.Buffer
	buffer.WriteString(fmt.Sprintf("%s failed with:\n", moduleName))
	s := bufio.NewScanner(strings.NewReader(string(output)))
	for s.Scan() {
		buffer.WriteString(fmt.Sprintf("\t--> %s\n", s.Text()))
	}
	log.Println(buffer.String())
}
