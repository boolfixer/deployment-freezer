package e2e

import (
	"context"
	"fmt"
	"os/exec"
	"time"

	"github.com/onsi/ginkgo/v2"
)

// Namespace and deployment name of the controller-manager to stream logs from.
const (
	managerNamespace      = "deployment-freezer-system"
	managerDeploymentName = "deployment-freezer-controller-manager"
)

var controllerLogCancel context.CancelFunc

// StartControllerLogStreamer starts a background goroutine that follows the controller-manager logs
// and writes them to the Ginkgo writer. It automatically retries if the pod restarts or is not up yet.
func StartControllerLogStreamer() {
	ctx, cancel := context.WithCancel(context.Background())
	controllerLogCancel = cancel

	go func() {
		backoff := 2 * time.Second
		for {
			select {
			case <-ctx.Done():
				return
			default:
			}

			// Stream logs from the controller deployment; this avoids needing an exact pod name.
			cmd := exec.CommandContext(ctx,
				"kubectl", "logs",
				"-n", managerNamespace,
				"deploy/"+managerDeploymentName,
				"-c", "manager",
				"-f",
				"--since=2m",
			)
			// Stream live output into the test output.
			cmd.Stdout = ginkgo.GinkgoWriter
			cmd.Stderr = ginkgo.GinkgoWriter

			if err := cmd.Run(); err != nil {
				// If context is canceled, exit quietly.
				if ctx.Err() != nil {
					return
				}
				_, _ = fmt.Fprintf(ginkgo.GinkgoWriter, "controller log streamer exited: %v; retrying in %s\n", err, backoff)
				time.Sleep(backoff)
				if backoff < 10*time.Second {
					backoff *= 2
				}
				continue
			}

			// If logs finished without error (e.g., pod rotated), short sleep and retry.
			time.Sleep(2 * time.Second)
		}
	}()
}

// StopControllerLogStreamer stops the background log streaming goroutine.
func StopControllerLogStreamer() {
	if controllerLogCancel != nil {
		controllerLogCancel()
	}
}
