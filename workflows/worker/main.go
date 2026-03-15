package main

import (
	"log"
	"net/http"
	"os"
	"time"

	"go.temporal.io/sdk/client"
	"go.temporal.io/sdk/worker"

	"compliance-platform/workflows"
)

func main() {
	hostPort := os.Getenv("TEMPORAL_HOST_PORT")
	if hostPort == "" {
		hostPort = "localhost:7233"
	}

	baseURL := os.Getenv("ENCORE_BASE_URL")
	if baseURL == "" {
		baseURL = "http://localhost:4000"
	}

	log.Printf("Connecting to Temporal at %s", hostPort)
	log.Printf("Encore base URL: %s", baseURL)

	c, err := client.Dial(client.Options{
		HostPort: hostPort,
	})
	if err != nil {
		log.Fatalf("Failed to connect to Temporal: %v", err)
	}
	defer c.Close()

	w := worker.New(c, "contact-queue", worker.Options{})

	// Register workflows.
	w.RegisterWorkflow(workflows.ContactWorkflow)
	w.RegisterWorkflow(workflows.PaymentPlanWorkflow)

	// Register activities with HTTP client for Encore API calls.
	activities := &workflows.Activities{
		Client: &http.Client{
			Timeout: 10 * time.Second,
		},
		BaseURL: baseURL,
	}
	w.RegisterActivity(activities)

	log.Println("Starting Temporal worker on task queue 'contact-queue'")
	if err := w.Run(worker.InterruptCh()); err != nil {
		log.Fatalf("Worker failed: %v", err)
	}
	log.Println("Temporal worker stopped")
}
