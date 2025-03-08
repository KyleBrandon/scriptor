package stacks

import (
	"github.com/KyleBrandon/scriptor/pkg/database"
	"github.com/KyleBrandon/scriptor/pkg/types"
	"github.com/aws/aws-cdk-go/awscdk/v2"
	"github.com/aws/aws-cdk-go/awscdk/v2/awsdynamodb"
	"github.com/aws/aws-cdk-go/awscdk/v2/awss3"
	"github.com/aws/aws-cdk-go/awscdk/v2/awssecretsmanager"
	"github.com/aws/jsii-runtime-go"
)

func (cfg *CdkScriptorConfig) initializeSecretsManager(stack awscdk.Stack) {

	// Reference an existing secret in AWS Secrets Manager
	cfg.GoogleServiceKeySecret = awssecretsmanager.Secret_FromSecretNameV2(stack, jsii.String(types.GOOGLE_SERVICE_KEY_SECRET), jsii.String(types.GOOGLE_SERVICE_KEY_SECRET))

	// Reference an existing secret in AWS Secrets Manager
	cfg.DefaultFoldersSecret = awssecretsmanager.Secret_FromSecretNameV2(stack, jsii.String(types.DEFAULT_GOOGLE_FOLDER_LOCATIONS_SECRET), jsii.String(types.DEFAULT_GOOGLE_FOLDER_LOCATIONS_SECRET))

}

func (cfg *CdkScriptorConfig) initializeDynamoDB(stack awscdk.Stack) {
	// create table for the Google Drive watch channels
	cfg.watchChannelTable = awsdynamodb.NewTable(stack, jsii.String(database.WATCH_CHANNEL_TABLE_NAME),
		&awsdynamodb.TableProps{
			TableName: jsii.String(database.WATCH_CHANNEL_TABLE_NAME),
			PartitionKey: &awsdynamodb.Attribute{
				Name: jsii.String("folder_id"),
				Type: awsdynamodb.AttributeType_STRING,
			},
		})

	// Add a GSI to query by ChannelID
	cfg.watchChannelTable.AddGlobalSecondaryIndex(&awsdynamodb.GlobalSecondaryIndexProps{
		IndexName: jsii.String("ChannelIDIndex"),
		PartitionKey: &awsdynamodb.Attribute{
			Name: jsii.String("channel_id"),
			Type: awsdynamodb.AttributeType_STRING,
		},
		ProjectionType: awsdynamodb.ProjectionType_ALL,
	})

	// Add a GSI to query by ChannelID
	cfg.watchChannelTable.AddGlobalSecondaryIndex(&awsdynamodb.GlobalSecondaryIndexProps{
		IndexName: jsii.String("ExpiresAtIndex"),
		PartitionKey: &awsdynamodb.Attribute{
			Name: jsii.String("expires_at"),
			Type: awsdynamodb.AttributeType_NUMBER,
		},
		ProjectionType: awsdynamodb.ProjectionType_ALL,
	})

	// register the Document table
	cfg.documentTable = awsdynamodb.NewTable(stack, jsii.String("Document"), &awsdynamodb.TableProps{
		TableName: jsii.String("document"),
		PartitionKey: &awsdynamodb.Attribute{
			Name: jsii.String("id"),
			Type: awsdynamodb.AttributeType_STRING,
		},
	})

	cfg.documentTable.AddGlobalSecondaryIndex(&awsdynamodb.GlobalSecondaryIndexProps{
		IndexName: jsii.String("StatusIndex"),
		PartitionKey: &awsdynamodb.Attribute{
			Name: jsii.String("status"),
			Type: awsdynamodb.AttributeType_STRING,
		},
	})
}

func (cfg *CdkScriptorConfig) initializeS3Buckets(stack awscdk.Stack) {
	bucketProps := awss3.BucketProps{
		BucketName:        jsii.String("scriptor-document-staging"),
		Versioned:         jsii.Bool(true),
		RemovalPolicy:     awscdk.RemovalPolicy_RETAIN,
		AutoDeleteObjects: jsii.Bool(false),
		BlockPublicAccess: awss3.BlockPublicAccess_BLOCK_ALL(),
		Encryption:        awss3.BucketEncryption_S3_MANAGED,
		// LifecycleRules: &[]*awss3.LifecycleRule{
		// 	Expiration: awscdk.Duration_Days(jsii.Number(30)),
		// },
	}
	cfg.documentBucket = awss3.NewBucket(stack, jsii.String("scriptorDocumentStagingBucket"), &bucketProps)
}

func (cfg *CdkScriptorConfig) NewResourcesStack(id string) awscdk.Stack {
	stack := awscdk.NewStack(cfg.App, &id, &cfg.Props.StackProps)

	cfg.initializeSecretsManager(stack)
	cfg.initializeDynamoDB(stack)
	cfg.initializeS3Buckets(stack)

	return stack

}
