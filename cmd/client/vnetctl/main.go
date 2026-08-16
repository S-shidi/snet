package main

import (
	"bytes"
	"encoding/json"
	"flag"
	"fmt"
	"io"
	"net/http"
	"os"
)

type ctlClient struct {
	addr string
	http *http.Client
}

func (c *ctlClient) do(method, path string, body any, out any) error {
	var rdr io.Reader
	if body != nil {
		b, _ := json.Marshal(body)
		rdr = bytes.NewReader(b)
	}
	req, err := http.NewRequest(method, c.addr+path, rdr)
	if err != nil {
		return err
	}
	if body != nil {
		req.Header.Set("Content-Type", "application/json")
	}
	resp, err := c.http.Do(req)
	if err != nil {
		return err
	}
	defer resp.Body.Close()
	data, _ := io.ReadAll(resp.Body)
	if resp.StatusCode >= 400 {
		var e map[string]string
		_ = json.Unmarshal(data, &e)
		return fmt.Errorf("ctl %d: %s", resp.StatusCode, e["error"])
	}
	if out != nil && len(data) > 0 {
		return json.Unmarshal(data, out)
	}
	return nil
}

func main() {
	ctl := flag.String("ctl", "http://127.0.0.1:19432", "daemon control API address")
	flag.Parse()
	args := flag.Args()
	if len(args) == 0 {
		usage()
	}

	c := &ctlClient{addr: *ctl, http: &http.Client{}}
	switch args[0] {
	case "create":
		fs := flag.NewFlagSet("create", flag.ExitOnError)
		server := fs.String("server", "http://127.0.0.1:8080", "coordination server")
		port := fs.Int("port", 51820, "wireguard listen port")
		name := fs.String("name", "", "network display name")
		subnet := fs.String("subnet", "", "network subnet (default: server auto-assigns)")
		approval := fs.Bool("approval", false, "require owner approval for new members")
		ca := fs.String("ca-path", "", "path to pinned server TLS certificate (PEM)")
		fs.Parse(args[1:])
		var out map[string]any
		if err := c.do(http.MethodPost, "/ctl/create", map[string]any{"server": *server, "port": *port, "name": *name, "subnet": *subnet, "approvalRequired": *approval, "ca": *ca}, &out); err != nil {
			fatal(err)
		}
		printJSON(out)

	case "join":
		fs := flag.NewFlagSet("join", flag.ExitOnError)
		server := fs.String("server", "http://127.0.0.1:8080", "coordination server")
		port := fs.Int("port", 51820, "wireguard listen port")
		link := fs.String("link", "", "vnet:// join link")
		nid := fs.String("nid", "", "network id")
		code := fs.String("code", "", "pairing code")
		ca := fs.String("ca-path", "", "path to pinned server TLS certificate (PEM)")
		fs.Parse(args[1:])
		var out map[string]any
		if err := c.do(http.MethodPost, "/ctl/join", map[string]any{"server": *server, "port": *port, "link": *link, "nid": *nid, "code": *code, "ca": *ca}, &out); err != nil {
			fatal(err)
		}
		printJSON(out)

	case "status":
		var out map[string]any
		if err := c.do(http.MethodGet, "/ctl/status", nil, &out); err != nil {
			fatal(err)
		}
		printJSON(out)

	case "networks":
		var out map[string]any
		if err := c.do(http.MethodGet, "/ctl/status", nil, &out); err != nil {
			fatal(err)
		}
		printJSON(out["networks"])

	case "leave":
		fs := flag.NewFlagSet("leave", flag.ExitOnError)
		nid := fs.String("nid", "", "network id (empty leaves all)")
		fs.Parse(args[1:])
		if err := c.do(http.MethodPost, "/ctl/leave", map[string]any{"nid": *nid}, nil); err != nil {
			fatal(err)
		}
		fmt.Println("stopped")

	case "rejoin":
		fs := flag.NewFlagSet("rejoin", flag.ExitOnError)
		nid := fs.String("nid", "", "network id")
		fs.Parse(args[1:])
		if *nid == "" {
			fmt.Fprintln(os.Stderr, "usage: vnetctl rejoin --nid <networkId>")
			os.Exit(2)
		}
		if err := c.do(http.MethodPost, "/ctl/rejoin", map[string]any{"nid": *nid}, nil); err != nil {
			fatal(err)
		}
		fmt.Println("restarted")

	case "remove":
		fs := flag.NewFlagSet("remove", flag.ExitOnError)
		nid := fs.String("nid", "", "network id")
		fs.Parse(args[1:])
		if *nid == "" {
			fmt.Fprintln(os.Stderr, "usage: vnetctl remove --nid <networkId>")
			os.Exit(2)
		}
		if err := c.do(http.MethodPost, "/ctl/remove", map[string]any{"nid": *nid}, nil); err != nil {
			fatal(err)
		}
		fmt.Println("removed")

	case "rename":
		fs := flag.NewFlagSet("rename", flag.ExitOnError)
		nid := fs.String("nid", "", "network id")
		name := fs.String("name", "", "new display name")
		fs.Parse(args[1:])
		if *nid == "" || *name == "" {
			fmt.Fprintln(os.Stderr, "usage: vnetctl rename --nid <networkId> --name <name>")
			os.Exit(2)
		}
		if err := c.do(http.MethodPost, "/ctl/rename", map[string]any{"nid": *nid, "name": *name}, nil); err != nil {
			fatal(err)
		}
		fmt.Println("renamed")

	case "settings":
		fs := flag.NewFlagSet("settings", flag.ExitOnError)
		nid := fs.String("nid", "", "network id")
		name := fs.String("name", "", "new display name")
		subnet := fs.String("subnet", "", "new subnet (re-allocates all member IPs)")
		approval := fs.Bool("approval", false, "require owner approval for new members")
		approvalSet := fs.Bool("set-approval", false, "apply the -approval value")
		fs.Parse(args[1:])
		if *nid == "" {
			fmt.Fprintln(os.Stderr, "usage: vnetctl settings --nid <networkId> [--name NAME] [--subnet CIDR] [--set-approval [--approval BOOL]]")
			os.Exit(2)
		}
		body := map[string]any{"nid": *nid, "name": *name, "subnet": *subnet}
		if *approvalSet {
			body["approvalRequired"] = *approval
		}
		if err := c.do(http.MethodPost, "/ctl/settings", body, nil); err != nil {
			fatal(err)
		}
		fmt.Println("updated")

	case "approve":
		fs := flag.NewFlagSet("approve", flag.ExitOnError)
		nid := fs.String("nid", "", "network id")
		pending := fs.String("pending", "", "pending request id")
		fs.Parse(args[1:])
		if *nid == "" || *pending == "" {
			fmt.Fprintln(os.Stderr, "usage: vnetctl approve --nid <networkId> --pending <pendingId>")
			os.Exit(2)
		}
		var out map[string]any
		if err := c.do(http.MethodPost, "/ctl/approve", map[string]any{"nid": *nid, "pendingId": *pending}, &out); err != nil {
			fatal(err)
		}
		printJSON(out)

	case "deny":
		fs := flag.NewFlagSet("deny", flag.ExitOnError)
		nid := fs.String("nid", "", "network id")
		pending := fs.String("pending", "", "pending request id")
		fs.Parse(args[1:])
		if *nid == "" || *pending == "" {
			fmt.Fprintln(os.Stderr, "usage: vnetctl deny --nid <networkId> --pending <pendingId>")
			os.Exit(2)
		}
		if err := c.do(http.MethodPost, "/ctl/deny", map[string]any{"nid": *nid, "pendingId": *pending}, nil); err != nil {
			fatal(err)
		}
		fmt.Println("denied")

	case "cancel-pending":
		fs := flag.NewFlagSet("cancel-pending", flag.ExitOnError)
		pending := fs.String("pending", "", "pending request id")
		fs.Parse(args[1:])
		if *pending == "" {
			fmt.Fprintln(os.Stderr, "usage: vnetctl cancel-pending --pending <pendingId>")
			os.Exit(2)
		}
		if err := c.do(http.MethodPost, "/ctl/cancel-pending", map[string]any{"pendingId": *pending}, nil); err != nil {
			fatal(err)
		}
		fmt.Println("cancelled")

	case "delete":
		fs := flag.NewFlagSet("delete", flag.ExitOnError)
		nid := fs.String("nid", "", "network id")
		fs.Parse(args[1:])
		if *nid == "" {
			fmt.Fprintln(os.Stderr, "usage: vnetctl delete --nid <networkId>")
			os.Exit(2)
		}
		if err := c.do(http.MethodPost, "/ctl/delete", map[string]any{"nid": *nid}, nil); err != nil {
			fatal(err)
		}
		fmt.Println("deleted")

	case "kick":
		fs := flag.NewFlagSet("kick", flag.ExitOnError)
		nid := fs.String("nid", "", "network id")
		node := fs.String("node", "", "node id to kick")
		fs.Parse(args[1:])
		if *nid == "" || *node == "" {
			fmt.Fprintln(os.Stderr, "usage: vnetctl kick --nid <networkId> --node <nodeId>")
			os.Exit(2)
		}
		if err := c.do(http.MethodPost, "/ctl/kick", map[string]any{"nid": *nid, "nodeId": *node}, nil); err != nil {
			fatal(err)
		}
		fmt.Println("kicked")

	case "reset-code":
		fs := flag.NewFlagSet("reset-code", flag.ExitOnError)
		nid := fs.String("nid", "", "network id")
		fs.Parse(args[1:])
		if *nid == "" {
			fmt.Fprintln(os.Stderr, "usage: vnetctl reset-code --nid <networkId>")
			os.Exit(2)
		}
		var out map[string]any
		if err := c.do(http.MethodPost, "/ctl/reset-code", map[string]any{"nid": *nid}, &out); err != nil {
			fatal(err)
		}
		printJSON(out)

	case "netinfo":
		fs := flag.NewFlagSet("netinfo", flag.ExitOnError)
		nid := fs.String("nid", "", "network id")
		fs.Parse(args[1:])
		if *nid == "" {
			fmt.Fprintln(os.Stderr, "usage: vnetctl netinfo --nid <networkId>")
			os.Exit(2)
		}
		var out map[string]any
		if err := c.do(http.MethodGet, "/ctl/netinfo?nid="+*nid, nil, &out); err != nil {
			fatal(err)
		}
		printJSON(out)

	case "device-id":
		fs := flag.NewFlagSet("device-id", flag.ExitOnError)
		set := fs.String("set", "", "replace the device id (advanced)")
		fs.Parse(args[1:])
		body := map[string]any{}
		if *set != "" {
			body["deviceId"] = *set
		}
		var out map[string]any
		if err := c.do(http.MethodPost, "/ctl/device-id", body, &out); err != nil {
			fatal(err)
		}
		printJSON(out)

	case "claim":
		fs := flag.NewFlagSet("claim", flag.ExitOnError)
		nid := fs.String("nid", "", "network id")
		fs.Parse(args[1:])
		if *nid == "" {
			fmt.Fprintln(os.Stderr, "usage: vnetctl claim --nid <networkId>")
			os.Exit(2)
		}
		if err := c.do(http.MethodPost, "/ctl/claim", map[string]any{"nid": *nid}, nil); err != nil {
			fatal(err)
		}
		fmt.Println("claimed")

	case "bind":
		fs := flag.NewFlagSet("bind", flag.ExitOnError)
		server := fs.String("server", "", "coordination server (required)")
		code := fs.String("code", "", "device authorization code (required)")
		ca := fs.String("ca-path", "", "path to pinned server TLS certificate (PEM)")
		fs.Parse(args[1:])
		if *server == "" || *code == "" {
			fmt.Fprintln(os.Stderr, "usage: vnetctl bind --server URL --code CODE [--ca-path PATH]")
			os.Exit(2)
		}
		var out map[string]any
		if err := c.do(http.MethodPost, "/ctl/bind", map[string]any{"server": *server, "code": *code, "ca": *ca}, &out); err != nil {
			fatal(err)
		}
		printJSON(out)

	default:
		usage()
	}
}

func printJSON(v any) {
	b, _ := json.MarshalIndent(v, "", "  ")
	fmt.Println(string(b))
}

func usage() {
	fmt.Fprintln(os.Stderr, `usage: vnetctl [--ctl URL] <command> [flags]

commands:
  create     --server URL [--port N] [--name NAME] [--subnet CIDR] [--approval]
  join       --link vnet:// | --nid NID --code CODE [--server URL]
  status
  networks
  leave      --nid NID (empty leaves all)
  rejoin     --nid NID
  remove     --nid NID
  rename     --nid NID --name NAME
  settings   --nid NID [--name NAME] [--subnet CIDR] [--set-approval [--approval BOOL]]
  approve    --nid NID --pending PENDINGID
  deny       --nid NID --pending PENDINGID
  cancel-pending --pending PENDINGID
  delete     --nid NID
  kick       --nid NID --node NODEID
  reset-code --nid NID
  netinfo    --nid NID
  device-id  [--set ID]
  claim      --nid NID
  bind       --server URL --code CODE [--ca-path PATH]`)
	os.Exit(2)
}

func fatal(err error) {
	fmt.Fprintln(os.Stderr, "error:", err)
	os.Exit(1)
}
