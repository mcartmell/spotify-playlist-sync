package sps

import (
	"encoding/base64"
	"encoding/json"
	"fmt"
	"io"
	"log"
	"net/http"
	"net/url"
	"strings"
	"sync"
)

func getSpotifyAccessToken() string {
	var accessToken string
	doneCh := make(chan bool)
	var once sync.Once
	// start a webserver on localhost:3000 to wait for the access code
	// from the spotify authorization page
	http.HandleFunc("/", func(w http.ResponseWriter, r *http.Request) {
		// get the access code from the url
		accessCode := r.URL.Query().Get("code")
		// exchange the access code for an access token
		once.Do(func() {
			accessToken = exchangeAccessCodeForAccessToken(accessCode)
		})
		// print the access token
		fmt.Println(accessToken)
		// close the webserver
		w.Write([]byte("You can close this window now."))
		doneCh <- true
	})
	// start the webserver
	go http.ListenAndServe(":3000", nil)
	// open the spotify authorization page in the default browser
	fmt.Printf("To continue, open the following link and approve the request:\n  %s?client_id=%s&response_type=code&redirect_uri=http://localhost:3000&scope=playlist-modify-public%%20playlist-modify-private\n", spotifyAuthorizeURL, clientID)
	// wait for the webserver to close
	<-doneCh
	return accessToken
}

func exchangeAccessCodeForAccessToken(accessCode string) string {
	// create a url with the access code
	tokenUrl := "https://accounts.spotify.com/api/token"
	data := url.Values{
		"grant_type":   {"authorization_code"},
		"code":         {accessCode},
		"redirect_uri": {"http://localhost:3000"},
		"client_id":    {clientID},
	}

	// create a new request
	req, err := http.NewRequest("POST", tokenUrl, strings.NewReader(data.Encode()))
	if err != nil {
		log.Fatal(err)
	}
	// set authorization header
	base64ClientAndSecret := base64.StdEncoding.EncodeToString([]byte(clientID + ":" + clientSecret))
	req.Header.Add("Authorization", "Basic "+base64ClientAndSecret)
	req.Header.Add("Content-Type", "application/x-www-form-urlencoded")
	// make the request
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		log.Fatal(err)
	}
	// check status
	if resp.StatusCode != http.StatusOK {
		// read the response body
		body, err := io.ReadAll(resp.Body)
		if err != nil {
			log.Fatal(err)
		}
		// print the response body
		fmt.Println(string(body))
		log.Fatal("status code is not 200 - access token")
	}
	// read the response
	body, err := io.ReadAll(resp.Body)
	if err != nil {
		log.Fatal(err)
	}
	// unmarshal the response into a struct
	var tokenResponse struct {
		AccessToken string `json:"access_token"`
	}
	err = json.Unmarshal(body, &tokenResponse)
	if err != nil {
		log.Fatal(err)
	}
	// return the access token
	return tokenResponse.AccessToken
}
