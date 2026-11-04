// Command datagw exposes the Nimbus data gateway compatibility surface.
package main

import (
	"fmt"
	"os"

	"github.com/Ricaardo/nimbus-os/datasources/internal/dataplaneapp"
)

func main() {
	if err := dataplaneapp.Run("datagw"); err != nil {
		fmt.Fprintln(os.Stderr, err)
		os.Exit(1)
	}
}
