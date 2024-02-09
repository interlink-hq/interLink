package main

import (
	"bufio"
	"context"
	"embed"
	"fmt"
	"os"
	"text/template"

	"golang.org/x/oauth2"
)

var (
	//go:embed templates
	templates embed.FS
)

// apiVersion: kustomize.config.k8s.io/v1beta1
// kind: Kustomization
// resources:
//   - ./deployment.yaml

type Resources struct {
	CPU    string
	Memory string
	Pods   string
}

type oauthStruct struct {
	RefreshToken string
	TokenURL     string
	ClientID     string
	ClientSecret string
}

type dataStruct struct {
	OAUTH         oauthStruct
	InterLinkURL  string
	InterLinkPort int
	VKName        string
	Namespace     string
	VKLimits      Resources
}

func evalManifest(path string, dataStruct dataStruct) (string, error) {
	//tmpl, err := template.ParseFS(templates, "*/deployment.yaml")
	tmpl, err := template.ParseFS(templates, path)
	if err != nil {
		return "", err
	}

	fDeploy, err := os.CreateTemp("", "tmpfile-") // in Go version older than 1.17 you can use ioutil.TempFile
	if err != nil {
		return "", err
	}

	// close and remove the temporary file at the end of the program
	defer fDeploy.Close()
	defer os.Remove(fDeploy.Name())

	err = tmpl.Execute(fDeploy, dataStruct)
	if err != nil {
		return "", err
	}

	deploymentYAML, err := os.ReadFile(fDeploy.Name())
	if err != nil {
		return "", err
	}

	return string(deploymentYAML), nil
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

	kubeletName := "test-vk"
	namespace := "interlink"
	interlinkURL := "http://localhost"

	data := dataStruct{
		OAUTH: oauthStruct{
			RefreshToken: token.RefreshToken,
			TokenURL:     "https://github.com/login/oauth/access_token",
			ClientID:     "Iv1.3150616a02483aa5",
			ClientSecret: "93efc7cc0c830dae6882da78a526ab981e12e5e6",
		},
		InterLinkURL:  interlinkURL,
		InterLinkPort: 128,
		VKName:        kubeletName,
		Namespace:     namespace,
		VKLimits: Resources{
			CPU:    "128",
			Memory: "256Gi",
			Pods:   "12800",
		},
	}

	namespaceYAML, err := evalManifest("templates/namespace.yaml", data)
	if err != nil {
		panic(err)
	}

	deploymentYAML, err := evalManifest("templates/deployment.yaml", data)
	if err != nil {
		panic(err)
	}

	configYAML, err := evalManifest("templates/configs.yaml", data)
	if err != nil {
		panic(err)
	}

	serviceaccountYAML, err := evalManifest("templates/service-account.yaml", data)
	if err != nil {
		panic(err)
	}

	manifests := []string{
		namespaceYAML,
		serviceaccountYAML,
		configYAML,
		deploymentYAML,
	}

	// Create a file and use bufio.NewWriter.
	f, err := os.Create("test.yaml")
	if err != nil {
		panic(err)
	}
	defer f.Close()
	w := bufio.NewWriter(f)

	for _, mnfst := range manifests {

		fmt.Fprint(w, mnfst)
		fmt.Fprint(w, "\n---\n")
	}

	w.Flush()

}
