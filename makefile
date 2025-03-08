lambdas: reregister_webhook_lambda workflow_download workflow_mathpix_process workflow_chatgpt_process workflow_upload 

reregister_webhook_lambda:
	@GOOS=linux GOARCH=amd64 go build -o ./bin/bootstrap ./lambdas/webhook_register
	@(cd bin && zip webhook_register_lambda.zip bootstrap)

workflow_download:
	@GOOS=linux GOARCH=amd64 go build -o ./bin/bootstrap ./lambdas/workflow_download
	@(cd bin && zip workflow_download.zip bootstrap)
	
workflow_mathpix_process:
	@GOOS=linux GOARCH=amd64 go build -o ./bin/bootstrap ./lambdas/workflow_mathpix_process
	@(cd bin && zip workflow_mathpix_process.zip bootstrap)

workflow_chatgpt_process:
	@GOOS=linux GOARCH=amd64 go build -o ./bin/bootstrap ./lambdas/workflow_chatgpt_process
	@(cd bin && zip workflow_chatgpt_process.zip bootstrap)

workflow_upload:
	@GOOS=linux GOARCH=amd64 go build -o ./bin/bootstrap ./lambdas/workflow_upload
	@(cd bin && zip workflow_upload.zip bootstrap)


cdk-diff: lambdas
	@(cd cdk && cdk diff)

cdk-deploy: cdk-diff
	@(cd cdk && cdk deploy --all)

clean:
	@rm ./bin/*
