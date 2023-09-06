package main

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
)

func httpGET(url string) ([]byte, error) {
	return httpRequest("GET", url, nil)
}

func httpPOST(url string, body any) ([]byte, error) {
	return httpRequest("POST", url, body)
}

func httpRequest(method string, url string, body any) (_ []byte, err error) {
	ctx, cancel := context.WithTimeout(context.Background(), config.Timeout)
	defer cancel()

	var bodyReader io.Reader
	var bodyJSON []byte
	if body != nil {
		bodyJSON, err = json.Marshal(body)
		if err != nil {
			return nil, err
		}
		bodyReader = bytes.NewReader(bodyJSON)
	}

	req, err := http.NewRequestWithContext(ctx, method, url, bodyReader)
	if err != nil {
		return nil, err
	}
	req.Header.Set("Authorization", "Bearer "+config.Token)

	debugf("-> %v %v\n", method, url)
	if bodyJSON != nil {
		debugf("   %v\n", string(bodyJSON))
	}
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		return nil, err
	}
	data, err := io.ReadAll(resp.Body)
	if err != nil {
		fmt.Println("failed to call http request:", err)
		return nil, err
	}
	if resp.StatusCode >= 200 && resp.StatusCode < 300 {
		debugf("<- %v\n", resp.Status)
		debugf("%v\n\n", string(data))
		return data, err
	}
	fmt.Println("failed to call http request:", url, resp.Status)
	fmt.Println(string(data))
	return data, errors.New(fmt.Sprintf("failed to call http request: (%v) %s", resp.Status, data))
}
