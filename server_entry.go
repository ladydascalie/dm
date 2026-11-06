package main

import "dm/internal/server"

// runServer is the temporary bridge to internal/server. The main
// unit deletes it.
func runServer(args []string) error { return server.Run(args) }
