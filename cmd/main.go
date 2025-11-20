package cmd

import (
	"log"
	"using-slicer/internal/api"
	"using-slicer/internal/orchestrator"
)

func main() {
	log.Println(">>> Starting MicroVM Agent...")

	mgr, err := orchestrator.New()
	if err != nil {
		log.Fatal(err)
	}

	srv := api.NewServer(mgr)
	log.Println(">>> Listening on :8080")
	srv.Run(":8080")
}
