package main

import "testing"

func TestCleanMarkdownResponseRemovesMarkdownFence(t *testing.T) {
	got := cleanMarkdownResponse("```markdown\n# Title\n\nBody\n```")

	if got != "# Title\n\nBody" {
		t.Fatalf("cleanMarkdownResponse returned %q", got)
	}
}

func TestCleanMarkdownResponseTrimsWhitespaceOnlyOutput(t *testing.T) {
	got := cleanMarkdownResponse("\n\n```markdown\n\n```\n\n")

	if got != "" {
		t.Fatalf("expected empty cleaned markdown, got %q", got)
	}
}
