package main

import (
	"bytes"
	"encoding/json"
	"errors"
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
	err := run(os.Args)
	if err != nil {
		fmt.Fprintln(os.Stderr, err.Error())
		os.Exit(1)
	}
}

func run(args []string) error {
	fs := flag.NewFlagSet("", flag.ExitOnError)
	configpath := fs.String("config", filepath.Join(os.Getenv("HOME"), ".config", "jiraattach", "config.json"), "path to config file")
	fs.Usage = func() {
		fmt.Fprintln(os.Stderr, usageMsg)
	}
	err := fs.Parse(args[1:])
	if err != nil {
		return err
	}

	args = fs.Args()
	if len(args) < 2 {
		return errors.New("key and path are required")
	}
	key, filepath := args[0], args[1]

	configfile, err := os.Open(*configpath)
	if err != nil {
		return fmt.Errorf("unable to open config file, %v", *configpath)
	}
	defer configfile.Close()
	config := &Config{}
	if err := json.NewDecoder(configfile).Decode(config); err != nil {
		return fmt.Errorf("failed to read config file, %v: %v\n", *configpath, err)
	}

	httpclient := &http.Client{
		Timeout: 5 * time.Second,
	}

	file, err := os.Open(filepath)
	if err != nil {
		return fmt.Errorf("error reading attachment, %v: %v\n", filepath, err)
	}
	defer file.Close()

	body := &bytes.Buffer{}
	w := multipart.NewWriter(body)
	part, err := w.CreateFormFile("file", filepath)
	if err != nil {
		return fmt.Errorf("error attaching file to form: %v\n", err)
	}
	_, err = io.Copy(part, file)
	if err != nil {
		return fmt.Errorf("error copying attachment into request: %v\n", err)
	}
	err = w.Close()
	if err != nil {
		return fmt.Errorf("error writing form body: %v\n", err)
	}

	req, err := http.NewRequest("POST", config.JiraURL+"/rest/api/2/issue/"+key+"/attachments", body)
	if err != nil {
		return fmt.Errorf("error creating request: %v\n", err)
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
		return fmt.Errorf("error sending request: %v\n", err)
	}
	defer resp.Body.Close()

	switch resp.StatusCode {
	case http.StatusOK:
		// do nothing, request was successful
	default:
		fmt.Fprintf(os.Stderr, "request failed with status code, %d\n", resp.StatusCode)
		respbody, err := ioutil.ReadAll(resp.Body)
		if err != nil {
			return fmt.Errorf("error reading error-response body: %v\n%v", err, string(respbody))
		}
	}
	return nil
}

type Config struct {
	JiraURL string `json:"jira_url"`
	Auth    string `json:"auth"`
}
