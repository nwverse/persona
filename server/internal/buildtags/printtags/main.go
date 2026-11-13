// Command printtags prints the -tags line of server/build-tags.txt (package buildtags):
//
//	cd server && go run ./internal/buildtags/printtags > build-tags.txt
package main

import (
	"fmt"

	"github.com/nwverse/persona/server/internal/buildtags"
)

func main() {
	fmt.Println(buildtags.Tags())
}
