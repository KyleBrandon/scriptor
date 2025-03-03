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

	myFunction := awslambda.NewFunction(stack, jsii.String("scriptorWebhookRegisterLambda"), &awslambda.FunctionProps{
		Runtime: awslambda.Runtime_PROVIDED_AL2023(),
		Code:    awslambda.AssetCode_FromAsset(jsii.String("../bin/webhook_register_lambda.zip"), nil),
		Handler: jsii.String("main"),
		Environment: &map[string]*string{
			"WEBHOOK_URL": jsii.String(cfg.WebhookURL),
		},
	})

	// grant the lambda permission to read the secrets
	cfg.GoogleServiceKeySecret.GrantRead(myFunction, nil)
	cfg.DefaultFoldersSecret.GrantRead(myFunction, nil)

	// grant the lambda permissions to read/write the watch channel table
	cfg.watchChannelTable.GrantReadWriteData(myFunction)

	// setup an event to trigger the lambda every 7 days
	rule := awsevents.NewRule(stack, jsii.String("WebhookRegisterSchedule"), &awsevents.RuleProps{
		Schedule: awsevents.Schedule_Rate(awscdk.Duration_Days(aws.Float64(1))),
	})

	rule.AddTarget(awseventstargets.NewLambdaFunction(myFunction, &awseventstargets.LambdaFunctionProps{}))

	return stack
}
