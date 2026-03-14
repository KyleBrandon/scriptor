package stacks

import (
	"github.com/aws/aws-cdk-go/awscdk/v2"
	"github.com/aws/aws-cdk-go/awscdk/v2/awslambda"
	"github.com/aws/aws-cdk-go/awscdk/v2/awslambdaeventsources"
	"github.com/aws/jsii-runtime-go"
)

func (cfg *CdkScriptorConfig) NewEmailIngestStack(id string) awscdk.Stack {
	stack := awscdk.NewStack(cfg.App, &id, &cfg.Props.StackProps)

	emailLambda := awslambda.NewFunction(
		stack,
		jsii.String("scriptorEmailIngestLambda"),
		&awslambda.FunctionProps{
			Runtime: awslambda.Runtime_PROVIDED_AL2023(),
			Code: awslambda.AssetCode_FromAsset(
				jsii.String("../bin/email_ingest.zip"),
				nil,
			),
			Handler: jsii.String("main"),
			Timeout: awscdk.Duration_Minutes(jsii.Number(5)),
			Environment: &map[string]*string{
				"STATE_MACHINE_ARN": jsii.String(
					*cfg.stateMachine.StateMachineArn(),
				),
			},
		},
	)

	eventSource := awslambdaeventsources.NewSqsEventSource(
		cfg.rawEmailQueue,
		&awslambdaeventsources.SqsEventSourceProps{
			BatchSize: jsii.Number(1),
		},
	)

	emailLambda.AddEventSource(eventSource)

	cfg.rawEmailBucket.GrantRead(emailLambda, nil)
	cfg.documentBucket.GrantReadWrite(emailLambda, nil)
	cfg.documentTable.GrantReadWriteData(emailLambda)
	cfg.documentProcessingStageTable.GrantReadWriteData(emailLambda)
	cfg.stateMachine.GrantStartExecution(emailLambda)

	return stack
}
