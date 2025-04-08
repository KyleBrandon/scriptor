package stacks

import (
	"github.com/aws/aws-cdk-go/awscdk/v2"
	"github.com/aws/aws-cdk-go/awscdk/v2/awslambda"
	"github.com/aws/aws-cdk-go/awscdk/v2/awslambdaeventsources"
	"github.com/aws/jsii-runtime-go"
)

func (cfg *CdkScriptorConfig) NewSQSHandlerStack(id string) awscdk.Stack {
	stack := awscdk.NewStack(cfg.App, &id, &cfg.Props.StackProps)
	// Define Lambda functions for workflow steps
	sqsLambda := awslambda.NewFunction(stack, jsii.String("scriptorSQSHandlerLambda"), &awslambda.FunctionProps{
		Runtime: awslambda.Runtime_PROVIDED_AL2(),
		Code:    awslambda.AssetCode_FromAsset(jsii.String("../bin/sqs_handler.zip"), nil), // Path to compiled Go binary
		Handler: jsii.String("main"),
		Timeout: awscdk.Duration_Minutes(jsii.Number(5)),
		Environment: &map[string]*string{
			"STATE_MACHINE_ARN": jsii.String(*cfg.stateMachine.StateMachineArn()),
		},
	})

	// setup an event source for the document SQS queue
	eventSource := awslambdaeventsources.NewSqsEventSource(cfg.documentQueue, &awslambdaeventsources.SqsEventSourceProps{
		BatchSize: jsii.Number(1),
	})

	// associate the SQS event source with the download lambda
	sqsLambda.AddEventSource(eventSource)

	// grant the lambda permission to read the Google Drive secret
	cfg.GoogleServiceKeySecret.GrantRead(sqsLambda, nil)

	// grant the lambda permission to start the state machine
	cfg.stateMachine.GrantStartExecution(sqsLambda)

	// grant the lambda r/w permissions to the watch channel lock table
	cfg.watchChannelLockTable.GrantReadWriteData(sqsLambda)

	// grant the lambda r/w permissions to the document table
	cfg.documentTable.GrantReadWriteData(sqsLambda)

	return stack
}
