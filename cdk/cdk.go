package main

import (
	"github.com/KyleBrandon/scriptor/cdk/stacks"
	"github.com/aws/jsii-runtime-go"
)

func main() {
	defer jsii.Close()

	cfg := stacks.NewCdkScriptorConfig()
	cfg.NewResourcesStack("ScriptorResourcesStack")
	cfg.NewWebhookHandlerStack("ScriptorWebhookProcessing")
	cfg.NewWebHookRegisterStack("ScriptorWebHookReRegisterStack")
	cfg.NewDocumentWorkflowStack("ScriptorDocumentWorkflow")
	cfg.NewSQSHandlerStack("ScrptorSQSHandlerStack")

	cfg.App.Synth(nil)
}
