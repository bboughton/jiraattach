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

  Attach the file at the given path to an issue. A comment will
  automatically be added to the issue with a link to the attachment.

ARGS

  key - The key of the Jira Issue to attach files to.

  path - Path to file to attach to Jira Issue.

OPTIONS

  -config       Path to config file, defaults to ~/.config/jiraattach/config.json.
  -no-comment   Don't create comment with link to attachment

CONFIG

  The config file must be a JSON formated file and contain the following properties.

  jira_url - URL for the Jira instance.

  auth - API authentication credentials. The expected format is 'username:password'.
`
)

func main() {
	err := run(os.Stderr, os.Args)
	if err != nil {
		fmt.Fprintln(os.Stderr, err.Error())
		os.Exit(1)
	}
}

func run(stderr io.Writer, args []string) error {
	fs := flag.NewFlagSet("", flag.ExitOnError)
	configpath := fs.String("config", defaultConfigPath(), "path to config file")
	nocomment := fs.Bool("no-comment", false, "don't create comment with link to attachment")
	fs.Usage = func() { fmt.Fprintln(stderr, usageMsg) }
	err := fs.Parse(args[1:])
	if err != nil {
		return err
	}

	args = fs.Args()
	if len(args) < 2 {
		return errors.New("key and path are required")
	}
	key, filepath := args[0], args[1]

	config, err := loadConfig(*configpath)

	httpclient := http.Client{
		Timeout: 5 * time.Second,
	}

	attachment, err := jiraAttachFile(&httpclient, config.JiraURL, config.Auth, key, filepath)
	if err != nil {
		return err
	}

	if *nocomment {
		// comments are opt-out
		return nil
	}

	return jiraComment(&httpclient, config.JiraURL, config.Auth, key, fmt.Sprintf("File attached: [%v|%v]", attachment.Filename, attachment.Content))
}

type Config struct {
	JiraURL string `json:"jira_url"`
	Auth    string `json:"auth"`
}

func defaultConfigPath() string {
	homedir, err := os.UserHomeDir()
	if err != nil {
		return ""
	}
	return filepath.Join(homedir, ".config", "jiraattach", "config.json")
}

func loadConfig(path string) (Config, error) {
	data, err := os.ReadFile(path)
	if err != nil {
		return Config{}, fmt.Errorf("unable to read config file: %w", err)
	}

	var config Config
	err = json.Unmarshal(data, &config)
	if err != nil {
		return Config{}, fmt.Errorf("failed to decode config: %w", err)
	}
	return config, nil
}

func jiraAttachFile(httpclient *http.Client, baseurl, auth, key, filepath string) (*Attachment, error) {
	body, contentType, err := createFileBody(filepath)
	if err != nil {
		return nil, err
	}

	req, err := http.NewRequest("POST", baseurl+"/rest/api/2/issue/"+key+"/attachments", body)
	if err != nil {
		return nil, fmt.Errorf("error creating request: %v\n", err)
	}
	req.Header.Set("Content-Type", contentType)
	req.Header.Set("X-Atlassian-Token", "nocheck") // Disable XSRF verification
	var user, pass string
	if strings.Contains(auth, ":") {
		parts := strings.Split(auth, ":")
		user, pass = parts[0], parts[1]
	}
	req.SetBasicAuth(user, pass)

	resp, err := httpclient.Do(req)
	if err != nil {
		return nil, fmt.Errorf("error sending request: %v\n", err)
	}
	defer resp.Body.Close()

	var attachments []Attachment
	switch resp.StatusCode {
	case http.StatusOK:
		respbody, err := ioutil.ReadAll(resp.Body)
		if err != nil {
			return nil, fmt.Errorf("error reading response body: %v\n%v", err, string(respbody))
		}
		err = json.Unmarshal(respbody, &attachments)
		if err != nil {
			return nil, err
		}

	default:
		respbody, err := ioutil.ReadAll(resp.Body)
		if err != nil {
			return nil, fmt.Errorf("error reading error-response body: %v\n%v", err, string(respbody))
		}
		return nil, fmt.Errorf("failed to add attachment, status_code=%d respbody=%v", resp.StatusCode, respbody)
	}
	if len(attachments) < 1 {
		return nil, fmt.Errorf("failed to add attachment for unknown reason")
	}
	return &attachments[0], nil
}

func createFileBody(path string) (*bytes.Buffer, string, error) {
	file, err := os.Open(path)
	if err != nil {
		return nil, "", fmt.Errorf("error reading attachment, %v: %v\n", path, err)
	}
	defer file.Close()

	var body bytes.Buffer
	w := multipart.NewWriter(&body)
	part, err := w.CreateFormFile("file", path)
	if err != nil {
		return nil, "", fmt.Errorf("error attaching file to form: %v\n", err)
	}
	_, err = io.Copy(part, file)
	if err != nil {
		return nil, "", fmt.Errorf("error copying attachment into request: %v\n", err)
	}

	err = w.Close()
	if err != nil {
		return nil, "", fmt.Errorf("error writing form body: %v\n", err)
	}
	return &body, w.FormDataContentType(), nil
}

func jiraComment(httpclient *http.Client, baseurl, auth, key, msg string) error {
	comment := Comment{
		Body: msg,
	}
	payload, err := json.Marshal(&comment)
	if err != nil {
		return err
	}
	body := bytes.NewBuffer(payload)

	req, err := http.NewRequest("POST", baseurl+"/rest/api/2/issue/"+key+"/comment", body)
	if err != nil {
		return fmt.Errorf("error creating request: %v\n", err)
	}
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("X-Atlassian-Token", "nocheck") // Disable XSRF verification
	var user, pass string
	if strings.Contains(auth, ":") {
		parts := strings.Split(auth, ":")
		user, pass = parts[0], parts[1]
	}
	req.SetBasicAuth(user, pass)

	resp, err := httpclient.Do(req)
	if err != nil {
		return fmt.Errorf("error sending request: %v\n", err)
	}
	defer resp.Body.Close()

	switch resp.StatusCode {
	case http.StatusCreated:
		// do nothing, request was successful
	default:
		respbody, err := ioutil.ReadAll(resp.Body)
		if err != nil {
			return fmt.Errorf("error reading error-response body: %v\n%v", err, string(respbody))
		}
		return fmt.Errorf("failed to add comment, status_code=%d respbody=%v", resp.StatusCode, respbody)
	}
	return nil
}

type Attachment struct {
	Content  string `json:"content"`
	Filename string `json:"filename"`
}

type Comment struct {
	Body string `json:"body"`
}
