package stacks

import (
	"github.com/aws/aws-cdk-go/awscdk/v2"
	"github.com/aws/aws-cdk-go/awscdk/v2/awsdynamodb"
	"github.com/aws/aws-cdk-go/awscdk/v2/awss3"
	"github.com/aws/aws-cdk-go/awscdk/v2/awssecretsmanager"
	"github.com/aws/aws-cdk-go/awscdk/v2/awssqs"
	"github.com/aws/aws-cdk-go/awscdk/v2/awsstepfunctions"
)

var SCRIPTOR_BASE_STACK string = "ScriptorInitStack"

type CdkStackProps struct {
	awscdk.StackProps
}

type CdkScriptorConfig struct {
	App        awscdk.App
	Props      *CdkStackProps
	WebhookURL string

	GoogleServiceKeySecret       awssecretsmanager.ISecret
	DefaultFoldersSecret         awssecretsmanager.ISecret
	MathpixSecrets               awssecretsmanager.ISecret
	ChatgptSecrets               awssecretsmanager.ISecret
	watchChannelTable            awsdynamodb.Table
	watchChannelLockTable        awsdynamodb.Table
	documentTable                awsdynamodb.Table
	documentProcessingStageTable awsdynamodb.Table
	documentBucket               awss3.Bucket
	documentQueue                awssqs.Queue
	stateMachine                 awsstepfunctions.StateMachine
}

func NewCdkScriptorConfig() *CdkScriptorConfig {
	cfg := &CdkScriptorConfig{}

	cfg.App = awscdk.NewApp(nil)

	cfg.Props = &CdkStackProps{
		StackProps: awscdk.StackProps{
			Env: env(),
		},
	}

	return cfg
}

// env determines the AWS environment (account+region) in which our stack is to
// be deployed. For more information see: https://docs.aws.amazon.com/cdk/latest/guide/environments.html
func env() *awscdk.Environment {
	// If unspecified, this stack will be "environment-agnostic".
	// Account/Region-dependent features and context lookups will not work, but a
	// single synthesized template can be deployed anywhere.
	//---------------------------------------------------------------------------
	return nil

	// Uncomment if you know exactly what account and region you want to deploy
	// the stack to. This is the recommendation for production stacks.
	//---------------------------------------------------------------------------
	// return &awscdk.Environment{
	//  Account: jsii.String("123456789012"),
	//  Region:  jsii.String("us-east-1"),
	// }

	// Uncomment to specialize this stack for the AWS Account and Region that are
	// implied by the current CLI configuration. This is recommended for dev
	// stacks.
	//---------------------------------------------------------------------------
	// return &awscdk.Environment{
	//  Account: jsii.String(os.Getenv("CDK_DEFAULT_ACCOUNT")),
	//  Region:  jsii.String(os.Getenv("CDK_DEFAULT_REGION")),
	// }
}
