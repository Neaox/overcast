package main

import (
	"context"
	"fmt"
	"log"
	"net/http"
	"net/http/httputil"

	"github.com/aws/aws-sdk-go-v2/aws"
	"github.com/aws/aws-sdk-go-v2/config"
	"github.com/aws/aws-sdk-go-v2/credentials"
	"github.com/aws/aws-sdk-go-v2/service/ec2"
)

type loggingTransport struct{ rt http.RoundTripper }

func (t *loggingTransport) RoundTrip(req *http.Request) (*http.Response, error) {
	dump, _ := httputil.DumpRequestOut(req, true)
	fmt.Printf("=== REQUEST ===\n%s\n", string(dump))
	resp, err := t.rt.RoundTrip(req)
	if resp != nil {
		dump2, _ := httputil.DumpResponse(resp, true)
		fmt.Printf("=== RESPONSE ===\n%s\n", string(dump2))
	}
	return resp, err
}

func main() {
	endpoint := "http://localhost:4566"
	ctx := context.Background()

	cfg, err := config.LoadDefaultConfig(ctx,
		config.WithRegion("us-east-1"),
		config.WithCredentialsProvider(credentials.NewStaticCredentialsProvider("test", "test", "")),
		config.WithHTTPClient(&http.Client{Transport: &loggingTransport{rt: http.DefaultTransport}}),
	)
	if err != nil {
		log.Fatal(err)
	}

	cfg.EndpointResolverWithOptions = aws.EndpointResolverWithOptionsFunc(
		func(service, region string, options ...interface{}) (aws.Endpoint, error) {
			return aws.Endpoint{URL: endpoint, HostnameImmutable: true}, nil
		},
	)

	ec2Client := ec2.NewFromConfig(cfg)

	// Create VPC
	vpcResp, err := ec2Client.CreateVpc(ctx, &ec2.CreateVpcInput{
		CidrBlock: aws.String("10.210.0.0/16"),
	})
	if err != nil {
		log.Fatal("CreateVpc:", err)
	}
	vpcID := *vpcResp.Vpc.VpcId
	fmt.Println("Created VPC:", vpcID)

	// Create IGW
	igwResp, err := ec2Client.CreateInternetGateway(ctx, &ec2.CreateInternetGatewayInput{})
	if err != nil {
		log.Fatal("CreateInternetGateway:", err)
	}
	igwID := *igwResp.InternetGateway.InternetGatewayId
	fmt.Println("Created IGW:", igwID)

	// Attach IGW - only log this request
	fmt.Println("\n\n--- AttachInternetGateway ---")
	_, err = ec2Client.AttachInternetGateway(ctx, &ec2.AttachInternetGatewayInput{
		InternetGatewayId: aws.String(igwID),
		VpcId:             aws.String(vpcID),
	})
	if err != nil {
		log.Fatal("AttachInternetGateway:", err)
	}
	fmt.Println("AttachInternetGateway: SUCCESS")
}
