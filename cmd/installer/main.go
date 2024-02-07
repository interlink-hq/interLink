package main

import (
	"context"
	"embed"
	"fmt"
	"net/url"
	"os"
	"text/template"

	"golang.org/x/oauth2"
)

var (
	//go:embed templates
	templates embed.FS
)

type Resources struct {
	CPU    string
	Memory string
	Pods   string
}

type dataStruct struct {
	Token         *oauth2.Token
	InterLinkURL  url.URL
	InterLinkPort int
	VKName        string
	VKLimits      Resources
}

func main() {

	ctx := context.Background()
	cfg := oauth2.Config{
		ClientID:     "Iv1.3150616a02483aa5",
		ClientSecret: "93efc7cc0c830dae6882da78a526ab981e12e5e6",
		Endpoint: oauth2.Endpoint{
			TokenURL:      "https://github.com/login/oauth/access_token",
			DeviceAuthURL: "https://github.com/login/device/code",
		},
		RedirectURL: "http://localhost:8080",
		Scopes:      []string{"read:user"},
	}

	response, err := cfg.DeviceAuth(ctx, oauth2.AccessTypeOffline)
	if err != nil {
		panic(err)
	}

	fmt.Printf("please enter code %s at %s\n", response.UserCode, response.VerificationURI)
	token, err := cfg.DeviceAccessToken(ctx, response, oauth2.AccessTypeOffline)
	if err != nil {
		panic(err)
	}
	fmt.Println(token.AccessToken)
	fmt.Println(token.RefreshToken)
	fmt.Println(token.Expiry)
	fmt.Println(token.TokenType)
	tmpl, err := template.ParseFS(templates, "*/deployment.yaml")
	if err != nil {
		panic(err)
	}

	f, err := os.CreateTemp("", "tmpfile-") // in Go version older than 1.17 you can use ioutil.TempFile
	if err != nil {
		panic(err)
	}

	// close and remove the temporary file at the end of the program
	defer f.Close()
	defer os.Remove(f.Name())

	data := dataStruct{
		Token:         token,
		InterLinkURL:  url.URL{},
		InterLinkPort: 128,
		VKName:        "test-vk",
		VKLimits: Resources{
			CPU:    "128",
			Memory: "256Gi",
			Pods:   "12800",
		},
	}

	//err = tmpl.Execute(f, data)
	err = tmpl.Execute(f, data)
	if err != nil {
		panic(err)
	}

	fmt.Println(f.Name())

	// TODO: embed template and deploy
	// TODO: option dump manifest

	// TODO: keep the following as reference for vk enhancement with
	// // automatic refresh on http client init to interlink APIs
	// manualToken := oauth2.Token{
	// 	AccessToken:  token.AccessToken,
	// 	TokenType:    token.TokenType,
	// 	RefreshToken: token.RefreshToken,
	// 	Expiry:       token.Expiry,
	// }
	//
	// fmt.Println(manualToken)
	//layout := "2024-02-07 07:06:42.994286 +0100 CET m=+28831.702329543"
	//token.Expiry.String()
	//  str := "Fri Sep 23 2017 15:38:22 GMT+0630"
	//  t, err := time.Parse(layout, str)
	//  if err != nil {
	//      WriteError(w, err)
	//      return
	//  }

}
