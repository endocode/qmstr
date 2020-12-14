package main

import (
	"context"
	"encoding/json"
	"fmt"
	"github.com/QMSTR/qmstr/lib/go-qmstr/service"
	"github.com/dgraph-io/dgo/v200"
	"github.com/dgraph-io/dgo/v200/protos/api"
	"github.com/golang/protobuf/jsonpb"
	"github.com/golang/protobuf/ptypes"
	"google.golang.org/grpc"
	"log"
	"os"
	"time"

	"github.com/streadway/amqp"
	"gopkg.in/confluentinc/confluent-kafka-go.v1/kafka"
)

func failOnError(err error, msg string) {
	if err != nil {
		log.Fatalf("%s: %s", msg, err)
	}
}

func declareQueue(ch *amqp.Channel, queue string) amqp.Queue {
	q, err := ch.QueueDeclare(
		queue, // name
		false, // durable
		false, // delete when unused
		false, // exclusive
		false, // no-wait
		nil,   // arguments
	)
	failOnError(err, "Failed to declare a queue")

	return q
}

func GetAllFileNodesMetadata(infotype string, datatype string) ([]*service.FileNode, error) {

	// Creating a DGraph client
	conn, err := grpc.Dial(os.Getenv("DB_ADDRESS"), grpc.WithInsecure())
	if err != nil {
		log.Fatal(err)
	}
	defer conn.Close()
	dgraphClient := dgo.NewDgraphClient(api.NewDgraphClient(conn))

	// Executing query
	const q = `
	query FilesMetadata($itype: string, $dtype: string) {
		FileNodes(func: has(fileNodeType)) @cascade {
			path
			fileData { # FileDataNodes
				hash
				additionalInfo @filter(eq(type, $itype))(orderdesc: confidenceScore, first: 1) { # InfoNodes
					type
					dataNodes @filter(eq(type, $dtype)) { # DataNodes
						data
					}
				}
			}
		}
	}`
	vars := map[string]string{"$itype": infotype, "$dtype": datatype}
	queryResult, err := dgraphClient.NewTxn().QueryWithVars(context.Background(), q, vars)
	if err != nil {
		return nil, err
	}

	// Unmarshaling to key-value map
	var m map[string]interface{}
	err = json.Unmarshal(queryResult.Json, &m)
	if err != nil {
		log.Fatal(err)
	}

	// Constructing the result
	result := []*service.FileNode{}
	fileNodes := m["FileNodes"].([]interface{})
	for _, fileNode := range fileNodes {
		path := fileNode.(map[string]interface{})["path"].(string)
		hash := fileNode.(map[string]interface{})["fileData"].(map[string]interface{})["hash"].(string)
		license := fileNode.(map[string]interface{})["fileData"].(map[string]interface{})["additionalInfo"].([]interface{})[0].(map[string]interface{})["dataNodes"].([]interface{})[0].(map[string]interface{})["data"].(string)
		result = append(result, &service.FileNode{
			Path: path,
			FileData: &service.FileNode_FileDataNode{
				Hash: hash,
				AdditionalInfo: []*service.InfoNode{{
					DataNodes: []*service.InfoNode_DataNode{{
						Data: license,
					}},
				}},
			},
		})
	}

	return result, nil
}

func collectLicenses() (string, error) {

	// Response object initialization
	response := &service.FastenResponse{}
	response.PluginName = "QMSTR"
	response.PluginVersion = "0.0.1"
	response.Input = &service.FastenResponse_FastenInput{}
	//response.Input.Product = // TODO fill in
	//response.Input.Forge = // TODO fill in
	//response.Input.Version = // TODO fill in
	response.CreatedAt = ptypes.TimestampNow()
	response.Payload = &service.FastenResponse_Report{Report: &service.FastenReport{
		PackageMetadata: &service.FastenReport_PackageMetadata{},
		FileMetadata:    []*service.FastenReport_FileMetadata{},
	}}

	// Retrieving FileNodes with metadata
	fileNodesMetadata, err := GetAllFileNodesMetadata("license", "name")
	if err != nil {
		// TODO return FastenError proto message
		return "", fmt.Errorf("couldn't get FileNodes, %v", err)
	}
	for _, fileNodeMetadata := range fileNodesMetadata {

		// Filling file response objects
		switch payload := response.Payload.(type) {
		case *service.FastenResponse_Report:
			payload.Report.FileMetadata = append(payload.Report.FileMetadata,
				&service.FastenReport_FileMetadata{
					FileName: fileNodeMetadata.GetPath(),
					Sha_1:    fileNodeMetadata.GetFileData().GetHash(),
					Licenses: []string{fileNodeMetadata.GetFileData().GetAdditionalInfo()[0].GetDataNodes()[0].GetData()},
				})
		default:
			// TODO return FastenError proto message
			return "", fmt.Errorf("response initialization error, %v", err)
		}

	}

	// Printing JSON to standard output for debugging purposes
	marshaler := jsonpb.Marshaler{}
	formattedResponse, err := marshaler.MarshalToString(response)
	if err != nil {
		return "", fmt.Errorf("couldn't format response to stdout")
	}
	log.Printf("Result: %v", formattedResponse)

	return formattedResponse, nil
}

func sendReportBack(kafkaAddress string, kafkaTopicName string, report string) {
	p, err := kafka.NewProducer(
		&kafka.ConfigMap{
			"bootstrap.servers": kafkaAddress,
		})
	failOnError(err, fmt.Sprintf("Couldn't connect to the Kafka instance at %v"))
	defer p.Close()

	// Delivery report handler for produced messages
	go func() {
		for e := range p.Events() {
			switch ev := e.(type) {
			case *kafka.Message:
				if ev.TopicPartition.Error != nil {
					fmt.Printf("Delivery failed: %v\n", ev.TopicPartition)
				} else {
					fmt.Printf("Delivered message to %v\n", ev.TopicPartition)
				}
			}
		}
	}()

	// Produce messages to topic (asynchronously)
	err = p.Produce(&kafka.Message{
		TopicPartition: kafka.TopicPartition{Topic: &kafkaTopicName, Partition: kafka.PartitionAny},
		Value:          []byte(report),
	}, nil)
	failOnError(err, "couldn't produce Kafka message")

	// Wait for message deliveries before shutting down
	p.Flush(15 * 1000)
}

func main() {

	// Connecting to RabbitMQ
	time.Sleep(30 * time.Second)
	rabbitMqAddress := os.Getenv("RABBITMQ_ADDRESS")
	log.Printf("Connecting to RabbitMQ at %v", rabbitMqAddress)
	conn, err := amqp.Dial(rabbitMqAddress)
	failOnError(err, "Failed to connect to RabbitMQ")
	defer conn.Close()
	ch, err := conn.Channel()
	failOnError(err, "Failed to open a channel")
	defer ch.Close()

	// Retrieving the correct queue
	queueName := "fasten-reporter"
	log.Printf("Listening to queue: %s", queueName)
	queue := declareQueue(ch, queueName)

	// Consuming a message
	msgs, err := ch.Consume(
		queue.Name, // queue
		"",         // consumer
		true,       // auto-ack
		false,      // exclusive
		false,      // no-local
		false,      // no-wait
		nil,        // args
	)
	failOnError(err, "Failed to register a consumer")

	// Sending back the module result
	responded := make(chan bool)
	go func() {
		for d := range msgs {
			log.Printf("Received a message: %s", d.Body)

			// Collecting licenses
			report, err := collectLicenses()
			failOnError(err, "FASTEN reporter failed")
			log.Printf("FASTEN reporter completed successfully")

			// Sending report to a Kafka topic
			kafkaAddress := os.Getenv("KAFKA_ADDRESS")
			kafkaTopicName := os.Getenv("REPORT_TOPIC_NAME")
			sendReportBack(kafkaAddress, kafkaTopicName, report)

			responded <- true
		}
	}()
	<-responded
}
