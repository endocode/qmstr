package cli

import (
	"errors"
	"log"
	"reflect"

	"github.com/QMSTR/qmstr/pkg/service"
	"github.com/spf13/cobra"
	"github.com/spf13/pflag"
	"golang.org/x/net/context"
)

var currentNode interface{}
var cmdFlags *pflag.FlagSet

var createCmd = &cobra.Command{
	Use:   "create",
	Short: "create a new node",
	Long:  "create a new node described by an node identifier",
	Run: func(cmd *cobra.Command, args []string) {
		setUpBuildService()
		cmdFlags = cmd.Flags()
		err := createNode(args[0], true)
		if err != nil {
			log.Fatalf("Failed to create node: %v", err)
		}
		tearDownServer()

	},
}

func init() {
	var err error
	generatedFlags := &pflag.FlagSet{}
	rootCmd.AddCommand(createCmd)
	err = generateFlags(&service.FileNode{}, generatedFlags)
	if err != nil {
		log.Fatalf("%v", err)
	}
	createCmd.Flags().AddFlagSet(generatedFlags)
	err = generateFlags(&service.PackageNode{}, generatedFlags)
	if err != nil {
		log.Fatalf("%v", err)
	}
	createCmd.Flags().AddFlagSet(generatedFlags)
}

func createNode(nodeIdent string, send bool) error {
	var err error
	currentNode, err = ParseNodeID(nodeIdent)
	if err != nil {
		return err
	}
	switch reflect.TypeOf(currentNode) {
	case reflect.TypeOf((*service.FileNode)(nil)):
		cmdFlags.Visit(visitFileNodeFlag)
		log.Printf("Got file node %v", currentNode.(*service.FileNode).Describe(true, ""))
		if send {
			stream, err := buildServiceClient.Build(context.Background())
			if err != nil {
				return err
			}
			stream.Send(currentNode.(*service.FileNode))
			br, err := stream.CloseAndRecv()
			if err != nil {
				return err
			}
			if !br.Success {
				return errors.New("sending filenode failed")
			}
			return nil
		}
	case reflect.TypeOf((*service.PackageNode)(nil)):
		cmdFlags.Visit(visitPkgNodeFlag)
		log.Printf("Got pkg node %v", currentNode.(*service.PackageNode).Describe(true))
		if send {
			log.Fatalf("Sending package nodes not yet supported")
		}
	}
	return nil
}