package main

import clientpkg "dm/internal/client"

// runClient is the temporary bridge to internal/client. The cli
// unit deletes it.
func runClient(name string) error { return clientpkg.Run(name) }
