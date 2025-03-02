lambdas: webhook_lambda reregister_webhook_lambda

webhook_lambda:
	@GOOS=linux GOARCH=amd64 go build -o ./bin/bootstrap ./lambdas/google_drive_webhook
	@(cd bin && zip gd_webhook_lambda.zip bootstrap)

reregister_webhook_lambda:
	@GOOS=linux GOARCH=amd64 go build -o ./bin/bootstrap ./lambdas/webhook_register
	@(cd bin && zip webhook_register_lambda.zip bootstrap)

cdk-diff: lambdas
	@(cd cdk && cdk diff)

cdk-deploy: cdk-diff
	@(cd cdk && cdk deploy --all)

clean:
	@rm ./bin/*
