package stacks

import (
	"github.com/KyleBrandon/scriptor/pkg/database"
	"github.com/KyleBrandon/scriptor/pkg/types"
	"github.com/aws/aws-cdk-go/awscdk/v2"
	"github.com/aws/aws-cdk-go/awscdk/v2/awsdynamodb"
	"github.com/aws/aws-cdk-go/awscdk/v2/awsevents"
	"github.com/aws/aws-cdk-go/awscdk/v2/awseventstargets"
	"github.com/aws/aws-cdk-go/awscdk/v2/awslambda"
	"github.com/aws/aws-cdk-go/awscdk/v2/awssecretsmanager"
	"github.com/aws/aws-sdk-go/aws"
	"github.com/aws/jsii-runtime-go"
)

func (cfg *CdkScriptorConfig) NewWebHookRegisterStack(id string) awscdk.Stack {

	stack := awscdk.NewStack(cfg.App, &id, &cfg.Props.StackProps)

	// Reference an existing secret in AWS Secrets Manager
	cfg.DefaultFoldersSecret = awssecretsmanager.Secret_FromSecretNameV2(stack, jsii.String(types.DEFAULT_GOOGLE_FOLDER_LOCATIONS_SECRET), jsii.String(types.DEFAULT_GOOGLE_FOLDER_LOCATIONS_SECRET))

	// create table for the Google Drive watch channels
	table := awsdynamodb.NewTable(stack, jsii.String(database.WATCH_CHANNEL_TABLE_NAME),
		&awsdynamodb.TableProps{
			TableName: jsii.String(database.WATCH_CHANNEL_TABLE_NAME),
			PartitionKey: &awsdynamodb.Attribute{
				Name: jsii.String("folder_id"),
				Type: awsdynamodb.AttributeType_STRING,
			},
		})

	// Add a GSI to query by ExpiresAt
	table.AddGlobalSecondaryIndex(&awsdynamodb.GlobalSecondaryIndexProps{
		IndexName: jsii.String("ExpiresAtIndex"),
		PartitionKey: &awsdynamodb.Attribute{
			Name: jsii.String("expires_at"),
			Type: awsdynamodb.AttributeType_NUMBER,
		},
		ProjectionType: awsdynamodb.ProjectionType_ALL,
	})

	// TODO: Pass in any initial watch folders for first initialization
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
	table.GrantReadWriteData(myFunction)

	// setup an event to trigger the lambda every 7 days
	rule := awsevents.NewRule(stack, jsii.String("WebhookRegisterSchedule"), &awsevents.RuleProps{
		Schedule: awsevents.Schedule_Rate(awscdk.Duration_Days(aws.Float64(1))),
	})

	rule.AddTarget(awseventstargets.NewLambdaFunction(myFunction, &awseventstargets.LambdaFunctionProps{}))

	return stack
}
