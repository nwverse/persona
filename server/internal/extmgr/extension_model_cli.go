package extmgr

import (
	"context"
	"errors"
	"fmt"
	"io"
	"net/url"
	"strings"

	"github.com/nwverse/persona/server/internal/extension"
)

// The extension model's commands (docs/extensions/extensions.md "Turning contributions on and off"), next to extensionUsage's.
const extensionModelUsage = `  contributions <id>                its contributions and their switches
  contribution <id> <point>/<cid> on|off
                                    switch one contribution (a connector's is the connector's switch)`

// extensionModelCommand runs the extension model's commands; done is false for a command that is not one of them.
func extensionModelCommand(ctx context.Context, c *extensionClient, cmd string, positional []string, stdout io.Writer) (done bool, err error) {
	switch cmd {
	case "contributions":
		if len(positional) < 1 {
			return true, errors.New(extensionModelUsage)
		}
		var d ExtensionDetail
		if err := c.do(ctx, "GET", "/api/v1/extensions/"+url.PathEscape(positional[0])+"/-/", nil, &d); err != nil {
			return true, err
		}
		printContributions(stdout, d.Contributions)
		return true, nil
	case "contribution":
		if len(positional) < 3 || (positional[2] != "on" && positional[2] != "off") {
			return true, errors.New(extensionModelUsage)
		}
		point, cid, ok := strings.Cut(positional[1], "/")
		if !ok {
			return true, fmt.Errorf("%q is not <point>/<id>", positional[1])
		}
		var d ExtensionDetail
		path := "/api/v1/extensions/" + url.PathEscape(positional[0]) + "/-/contributions/" + url.PathEscape(point) + "/" + url.PathEscape(cid)
		if err := c.do(ctx, "PUT", path, map[string]bool{"enabled": positional[2] == "on"}, &d); err != nil {
			return true, err
		}
		printContributions(stdout, d.Contributions)
		return true, nil
	}
	return false, nil
}

// printBundle says what installs with a preview: its dependencies and their state, the previews that come along
// (each with what it may do), and what no market has.
func printBundle(w io.Writer, pv *extension.Preview) {
	for _, d := range pv.Depends {
		state := map[string]string{extension.DepOK: "已装", extension.DepMissing: "没装", extension.DepDisabled: "停用着",
			extension.DepOld: "版本不够（" + d.Installed + "）"}[d.State]
		fmt.Fprintf(w, "要用：%s %s（%s）\n", d.ID, d.Version, state)
	}
	if len(pv.Bundle) > 0 {
		fmt.Fprintln(w, "一起装：")
	}
	for _, b := range pv.Bundle {
		verb := "安装"
		if b.Upgrade {
			verb = "升级到"
		}
		fmt.Fprintf(w, "  %s %s %s（%s）\n", verb, b.Manifest.ID, b.Manifest.Version, b.Manifest.Name)
		for _, l := range b.Permissions {
			fmt.Fprintf(w, "    · %s：%s\n", l.Title, l.Detail)
		}
	}
	for _, u := range pv.Unresolved {
		fmt.Fprintf(w, "找不到：%s\n", u)
	}
}

// bundleRefs are a preview's bundle as the install request sends it back.
func bundleRefs(pv *extension.Preview) []bundleRef {
	out := []bundleRef{}
	for _, b := range pv.Bundle {
		out = append(out, bundleRef{PreviewID: b.ID, Digest: b.Digest})
	}
	return out
}

func printContributions(w io.Writer, cs []ContributionView) {
	if len(cs) == 0 {
		fmt.Fprintln(w, "没有贡献")
	}
	for _, c := range cs {
		state := map[bool]string{true: "开", false: "关"}[c.Enabled]
		note := ""
		switch {
		case c.Via == "connector":
			note = "（跟着连接器的开关）"
		case !c.CanDisable:
			note = "（这里改不了）"
		}
		fmt.Fprintf(w, "%-28s %s  %s%s\n", c.Point+"/"+c.ID, state, c.Title, note)
	}
}
