package main

import (
	"bytes"
	"encoding/json"
	"flag"
	"fmt"
	"io"
	"io/ioutil"
	"mime/multipart"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"time"
)

const (
	usageMsg = `usage: jiraattach [-config=path] key path

ARGS

  key - The key of the Jira Issue to attach files to.

  path - Path to file to attach to Jira Issue.

OPTIONS

  -config - Path to config file, defaults to ~/.config/jiraattach/config.json.

CONFIG

  The config file must be a JSON formated file and contain the following properties.

  jira_url - URL for the Jira instance.

  auth - API authentication credentials. The expected format is 'username:password'.
`
)

func main() {
	configpath := flag.String("config", filepath.Join(os.Getenv("HOME"), ".config", "jiraattach", "config.json"), "path to config file")
	flag.Usage = func() {
		fmt.Fprintln(os.Stderr, usageMsg)
	}
	flag.Parse()

	args := flag.Args()
	if len(args) < 2 {
		fmt.Fprintln(os.Stderr, "key and path are required")
		os.Exit(2)
	}
	key, filepath := args[0], args[1]

	configfile, err := os.Open(*configpath)
	if err != nil {
		fmt.Fprintln(os.Stderr, "unable to open config file, %v", *configpath)
		os.Exit(2)
	}
	defer configfile.Close()
	config := &Config{}
	if err := json.NewDecoder(configfile).Decode(config); err != nil {
		fmt.Fprintf(os.Stderr, "failed to read config file, %v: %v\n", *configpath, err)
		os.Exit(2)
	}

	httpclient := &http.Client{
		Timeout: 5 * time.Second,
	}

	file, err := os.Open(filepath)
	if err != nil {
		fmt.Fprintf(os.Stderr, "error reading attachment, %v: %v\n", filepath, err)
		os.Exit(2)
	}
	defer file.Close()

	body := &bytes.Buffer{}
	w := multipart.NewWriter(body)
	part, err := w.CreateFormFile("file", filepath)
	if err != nil {
		fmt.Fprintf(os.Stderr, "error attaching file to form: %v\n", err)
		os.Exit(2)
	}
	_, err = io.Copy(part, file)
	if err != nil {
		fmt.Fprintf(os.Stderr, "error copying attachment into request: %v\n", err)
		os.Exit(2)
	}
	err = w.Close()
	if err != nil {
		fmt.Fprintf(os.Stderr, "error writing form body: %v\n", err)
		os.Exit(2)
	}

	req, err := http.NewRequest("POST", config.JiraURL+"/rest/api/2/issue/"+key+"/attachments", body)
	if err != nil {
		fmt.Fprintf(os.Stderr, "error creating request: %v\n", err)
		os.Exit(2)
	}
	req.Header.Set("Content-Type", w.FormDataContentType())
	req.Header.Set("X-Atlassian-Token", "nocheck") // Disable XSRF verification
	var user, pass string
	if strings.Contains(config.Auth, ":") {
		parts := strings.Split(config.Auth, ":")
		user, pass = parts[0], parts[1]
	}
	req.SetBasicAuth(user, pass)

	resp, err := httpclient.Do(req)
	if err != nil {
		fmt.Fprintf(os.Stderr, "error sending request: %v\n", err)
		os.Exit(2)
	}
	defer resp.Body.Close()

	switch resp.StatusCode {
	case http.StatusOK:
		// do nothing, request was successful
	default:
		fmt.Fprintf(os.Stderr, "request failed with status code, %d\n", resp.StatusCode)
		respbody, err := ioutil.ReadAll(resp.Body)
		if err != nil {
			fmt.Fprintf(os.Stderr, "error reading error-response body: %v\n", err)
		}
		fmt.Fprintln(os.Stderr, string(respbody))
		os.Exit(2)
	}
}

type Config struct {
	JiraURL string `json:"jira_url"`
	Auth    string `json:"auth"`
}
