package stacks

import (
	"github.com/aws/aws-cdk-go/awscdk/v2"
	"github.com/aws/aws-cdk-go/awscdk/v2/awsevents"
	"github.com/aws/aws-cdk-go/awscdk/v2/awseventstargets"
	"github.com/aws/aws-cdk-go/awscdk/v2/awslambda"
	"github.com/aws/aws-sdk-go/aws"
	"github.com/aws/jsii-runtime-go"
)

func (cfg *CdkScriptorConfig) NewWebHookRegisterStack(id string) awscdk.Stack {
	stack := awscdk.NewStack(cfg.App, &id, &cfg.Props.StackProps)

	myFunction := awslambda.NewFunction(
		stack,
		jsii.String("scriptorWebhookRegisterLambda"),
		&awslambda.FunctionProps{
			Runtime: awslambda.Runtime_PROVIDED_AL2023(),
			Code: awslambda.AssetCode_FromAsset(
				jsii.String("../bin/webhook_register.zip"),
				nil,
			),
			Handler: jsii.String("main"),
			Environment: &map[string]*string{
				"WEBHOOK_URL": jsii.String(cfg.WebhookURL),
			},
		},
	)

	// grant the lambda permission to read the Google Drive secret
	cfg.GoogleServiceKeySecret.GrantRead(myFunction, nil)

	// grant the lambda permission to read the default folder information
	cfg.DefaultFoldersSecret.GrantRead(myFunction, nil)

	// grant the lambda permissions to read/write the watch channel table
	cfg.watchChannelTable.GrantReadWriteData(myFunction)

	// grant the lambda permissions to read/write the watch channel lock table
	cfg.watchChannelLockTable.GrantReadWriteData(myFunction)

	// setup an event to trigger the lambda to renew the watch channel(s) every 20 hours
	rule := awsevents.NewRule(
		stack,
		jsii.String("WebhookRegisterSchedule"),
		&awsevents.RuleProps{
			Schedule: awsevents.Schedule_Rate(
				awscdk.Duration_Hours(aws.Float64(20)),
			),
		},
	)

	rule.AddTarget(
		awseventstargets.NewLambdaFunction(
			myFunction,
			&awseventstargets.LambdaFunctionProps{},
		),
	)

	return stack
}
