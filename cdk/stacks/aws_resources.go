package stacks

import (
	"github.com/KyleBrandon/scriptor/pkg/database"
	"github.com/KyleBrandon/scriptor/pkg/types"
	"github.com/aws/aws-cdk-go/awscdk/v2"
	"github.com/aws/aws-cdk-go/awscdk/v2/awsdynamodb"
	"github.com/aws/aws-cdk-go/awscdk/v2/awss3"
	"github.com/aws/aws-cdk-go/awscdk/v2/awssecretsmanager"
	"github.com/aws/aws-cdk-go/awscdk/v2/awssqs"
	"github.com/aws/jsii-runtime-go"
)

func (cfg *CdkScriptorConfig) initializeSecretsManager(stack awscdk.Stack) {

	// Reference an existing secret in AWS Secrets Manager
	cfg.GoogleServiceKeySecret = awssecretsmanager.Secret_FromSecretNameV2(stack, jsii.String(types.GOOGLE_SERVICE_SECRETS), jsii.String(types.GOOGLE_SERVICE_SECRETS))

	// Reference an existing secret in AWS Secrets Manager
	cfg.DefaultFoldersSecret = awssecretsmanager.Secret_FromSecretNameV2(stack, jsii.String(types.GOOGLE_FOLDER_DEFAULT_LOCATIONS_SECRETS), jsii.String(types.GOOGLE_FOLDER_DEFAULT_LOCATIONS_SECRETS))
	//
	// Reference an existing secret in AWS Secrets Manager
	cfg.MathpixSecrets = awssecretsmanager.Secret_FromSecretNameV2(stack, jsii.String(types.MATHPIX_SECRETS), jsii.String(types.MATHPIX_SECRETS))

	// Reference an existing secret in AWS Secrets Manager
	cfg.ChatgptSecrets = awssecretsmanager.Secret_FromSecretNameV2(stack, jsii.String(types.CHATGPT_SECRETS), jsii.String(types.CHATGPT_SECRETS))

}

func (cfg *CdkScriptorConfig) initializeWatchChannelLockTable(stack awscdk.Stack) {

	// create table for the Google Drive watch channels
	cfg.watchChannelLockTable = awsdynamodb.NewTable(stack, jsii.String("WatchChannelLockTable"),
		&awsdynamodb.TableProps{
			TableName: jsii.String(database.WATCH_CHANNEL_LOCK_TABLE),
			PartitionKey: &awsdynamodb.Attribute{
				Name: jsii.String("channel_id"),
				Type: awsdynamodb.AttributeType_STRING,
			},
			BillingMode: awsdynamodb.BillingMode_PAY_PER_REQUEST,
		})
}

func (cfg *CdkScriptorConfig) initializeWatchChannelTable(stack awscdk.Stack) {

	// create table for the Google Drive watch channels
	cfg.watchChannelTable = awsdynamodb.NewTable(stack, jsii.String("WatchChannelTable"),
		&awsdynamodb.TableProps{
			TableName: jsii.String(database.WATCH_CHANNEL_TABLE),
			PartitionKey: &awsdynamodb.Attribute{
				Name: jsii.String("folder_id"),
				Type: awsdynamodb.AttributeType_STRING,
			},
			BillingMode: awsdynamodb.BillingMode_PAY_PER_REQUEST,
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
}

func (cfg *CdkScriptorConfig) initializeDocumentTable(stack awscdk.Stack) {
	// register the Document table
	cfg.documentTable = awsdynamodb.NewTable(stack, jsii.String("DocumentsTable"), &awsdynamodb.TableProps{
		TableName: jsii.String(database.DOCUMENT_TABLE),
		PartitionKey: &awsdynamodb.Attribute{
			Name: jsii.String("id"),
			Type: awsdynamodb.AttributeType_STRING,
		},
		BillingMode: awsdynamodb.BillingMode_PAY_PER_REQUEST,
	})

	// Add a GSI to query by Google ID
	cfg.documentTable.AddGlobalSecondaryIndex(&awsdynamodb.GlobalSecondaryIndexProps{
		IndexName: jsii.String("GoogleFileIDIndex"),
		PartitionKey: &awsdynamodb.Attribute{
			Name: jsii.String("google_id"),
			Type: awsdynamodb.AttributeType_STRING,
		},
		ProjectionType: awsdynamodb.ProjectionType_ALL,
	})

	// register the DocumentProcessingStage table
	cfg.documentProcessingStageTable = awsdynamodb.NewTable(stack, jsii.String("DocumentProcessingStageTable"), &awsdynamodb.TableProps{
		TableName: jsii.String(database.DOCUMENT_PROCESSING_STAGE_TABLE),
		PartitionKey: &awsdynamodb.Attribute{
			Name: jsii.String("id"),
			Type: awsdynamodb.AttributeType_STRING,
		},
		SortKey: &awsdynamodb.Attribute{
			Name: jsii.String("stage"),
			Type: awsdynamodb.AttributeType_STRING,
		},
		BillingMode: awsdynamodb.BillingMode_PAY_PER_REQUEST,
	})

}

func (cfg *CdkScriptorConfig) initializeDynamoDB(stack awscdk.Stack) {
	cfg.initializeWatchChannelLockTable(stack)
	cfg.initializeWatchChannelTable(stack)
	cfg.initializeDocumentTable(stack)
}

func (cfg *CdkScriptorConfig) initializeS3Buckets(stack awscdk.Stack) {
	bucketProps := awss3.BucketProps{
		BucketName:        jsii.String(types.S3_BUCKET_NAME),
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

func (cfg *CdkScriptorConfig) initializeSQS(stack awscdk.Stack) {

	dlq := awssqs.NewQueue(stack, jsii.String("scriptorDocumentDLQ"), &awssqs.QueueProps{
		QueueName: jsii.String("ScriptorDocumentDLQ"),
	})

	cfg.documentQueue = awssqs.NewQueue(stack, jsii.String("scriptorDocumentQueue"), &awssqs.QueueProps{
		QueueName:              jsii.String("ScriptorDocumentQueue"),
		ReceiveMessageWaitTime: awscdk.Duration_Seconds(jsii.Number(10)),
		RetentionPeriod:        awscdk.Duration_Days(jsii.Number(4)),
		VisibilityTimeout:      awscdk.Duration_Minutes(jsii.Number(5)),
		DeadLetterQueue: &awssqs.DeadLetterQueue{
			Queue:           dlq,
			MaxReceiveCount: jsii.Number(5),
		},
	})
}

func (cfg *CdkScriptorConfig) NewResourcesStack(id string) awscdk.Stack {
	stack := awscdk.NewStack(cfg.App, &id, &cfg.Props.StackProps)

	cfg.initializeSecretsManager(stack)
	cfg.initializeDynamoDB(stack)
	cfg.initializeS3Buckets(stack)
	cfg.initializeSQS(stack)

	return stack

}
