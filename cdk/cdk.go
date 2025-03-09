package main

import (
	"github.com/KyleBrandon/scriptor/cdk/stacks"
	"github.com/aws/jsii-runtime-go"
)

func main() {
	defer jsii.Close()

	cfg := stacks.NewCdkScriptorConfig()
	cfg.NewResourcesStack("ScriptorResourcesStack")
	cfg.NewDocumentWorkflowStack("ScriptorDocumentWorkflow")
	cfg.NewWebHookRegisterStack("ScriptorWebHookReRegisterStack")

	cfg.App.Synth(nil)
}
