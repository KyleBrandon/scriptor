# Define Lambda names
LAMBDA_NAMES = \
	webhook_register \
	workflow_download \
	workflow_mathpix_process \
	workflow_chatgpt_process \
	workflow_upload

# Directories
BIN_DIR = ./bin
LAMBDA_DIR = ./lambdas

# Define the output zip files for each Lambda
LAMBDA_ZIPS = $(patsubst %, $(BIN_DIR)/%.zip, $(LAMBDA_NAMES))

# Default target: Build all lambdas
all: lambdas

# Build all lambdas only if needed
lambdas: $(LAMBDA_ZIPS)

# Pattern rule for building each Lambda
$(BIN_DIR)/%.zip: $(LAMBDA_DIR)/%/*.go
	@echo " Building Lambda: $*"
	@GOOS=linux GOARCH=amd64 go build -o $(BIN_DIR)/bootstrap $(LAMBDA_DIR)/$*
	@(cd $(BIN_DIR) && zip $*.zip bootstrap)
	@rm $(BIN_DIR)/bootstrap

# CDK operations
cdk-diff: lambdas
	@(cd cdk && cdk diff)

cdk-deploy: cdk-diff
	@(cd cdk && cdk deploy --all)

# Clean generated files
clean:
	@rm -f $(BIN_DIR)/*.zip


