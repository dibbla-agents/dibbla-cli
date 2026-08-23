package preflight

import (
	"strings"
	"testing"
)

func TestInContainer(t *testing.T) {
	t.Setenv(containerEnv, "")
	if InContainer() {
		t.Error("InContainer() = true with the env var unset")
	}

	t.Setenv(containerEnv, "1")
	if !InContainer() {
		t.Error("InContainer() = false with the env var set to 1")
	}
}

// A tool that is present must not error, in or out of the image — the image
// boundary is about what is missing, not about refusing to work.
func TestRequireToolPresent(t *testing.T) {
	t.Setenv(containerEnv, "1")
	if err := RequireTool("sh"); err != nil {
		t.Errorf("RequireTool(sh) = %v, want nil", err)
	}
}

// The whole point of the helper: inside the image the error has to name the
// image, or the user is left debugging a PATH problem that isn't one.
func TestRequireToolMissingInContainerNamesTheImage(t *testing.T) {
	t.Setenv(containerEnv, "1")
	err := RequireTool("definitely-not-a-real-binary")
	if err == nil {
		t.Fatal("RequireTool() = nil for a missing tool, want an error")
	}
	if !strings.Contains(err.Error(), "Dibbla CI image") {
		t.Errorf("error does not mention the image: %v", err)
	}
	if !strings.Contains(err.Error(), "deploy") {
		t.Errorf("error does not point at the commands that do work: %v", err)
	}
}

func TestRequireToolMissingOutsideContainerStaysPlain(t *testing.T) {
	t.Setenv(containerEnv, "")
	err := RequireTool("definitely-not-a-real-binary")
	if err == nil {
		t.Fatal("RequireTool() = nil for a missing tool, want an error")
	}
	if strings.Contains(err.Error(), "Dibbla CI image") {
		t.Errorf("error mentions the image outside it: %v", err)
	}
}
