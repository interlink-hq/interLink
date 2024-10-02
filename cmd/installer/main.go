package main

import (
	"bufio"
	"context"
	"embed"
	"fmt"
	"io"
	"io/fs"
	"log"
	"os"
	"text/template"

	"github.com/spf13/cobra"
	"golang.org/x/oauth2"
	"gopkg.in/yaml.v3"
)

var (
	// Used for flags.
	cfgFile   string
	outFolder string

	rootCmd = &cobra.Command{
		Use:   "ilctl",
		Short: "CLI to manage interLink deployment",
		Long:  `interLink cloud tools allows to extend kubernetes cluster over any remote resource`,
		RunE:  root,
	}
	//go:embed templates
	templates embed.FS
)

type Resources struct {
	CPU    string `yaml:"cpu"`
	Memory string `yaml:"memory"`
	Pods   string `yaml:"pods"`
}

type oauthStruct struct {
	Provider      string   `yaml:"provider"`
	GrantType     string   `default:"authorization_code" yaml:"grant_type"`
	Issuer        string   `yaml:"issuer,omitempty"`
	RefreshToken  string   `yaml:"refresh_token,omitempty"`
	Audience      string   `yaml:"audience,omitempty"`
	Group         string   `yaml:"group,omitempty"`
	GroupClaim    string   `default:"groups" yaml:"group_claim"`
	Scopes        []string `yaml:"scopes"`
	GitHUBUser    string   `yaml:"github_user"`
	TokenURL      string   `yaml:"token_url"`
	DeviceCodeURL string   `yaml:"device_code_url"`
	ClientID      string   `yaml:"client_id"`
	ClientSecret  string   `yaml:"client_secret"`
}

// TODO: insert in-cluster and socket option e.g. --> no need OAUTH
type dataStruct struct {
	InterLinkIP      string      `yaml:"interlink_ip"`
	InterLinkPort    int         `yaml:"interlink_port"`
	InterLinkVersion string      `yaml:"interlink_version"`
	VKName           string      `yaml:"kubelet_node_name"`
	Namespace        string      `yaml:"kubernetes_namespace,omitempty"`
	VKLimits         Resources   `yaml:"node_limits"`
	OAUTH            oauthStruct `yaml:"oauth,omitempty"`
}

func evalManifest(path string, dataStruct dataStruct) (string, error) {

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

func root(cmd *cobra.Command, args []string) error {
	var configCLI dataStruct

	onlyInit, err := cmd.Flags().GetBool("init")
	if err != nil {
		return err
	}

	if onlyInit {

		if _, err = os.Stat(cfgFile); err == nil {
			return fmt.Errorf("File config file exists. Please remove it before trying init again: %w", err)
		}

		dumpConfig := dataStruct{
			VKName:    "my-vk-node",
			Namespace: "interlink",
			VKLimits: Resources{
				CPU:    "10",
				Memory: "256Gi",
				Pods:   "10",
			},
			InterLinkIP:      "127.0.0.1",
			InterLinkPort:    8443,
			InterLinkVersion: "0.1.2",
			OAUTH: oauthStruct{
				ClientID:      "",
				ClientSecret:  "",
				Scopes:        []string{""},
				TokenURL:      "",
				DeviceCodeURL: "",
				Provider:      "github",
				GitHUBUser:    "myusername",
				Issuer:        "https://github.com/oauth",
			},
		}

		yamlData, err := yaml.Marshal(dumpConfig)
		if err != nil {
			fmt.Println(err)
			return err
		}

		fmt.Println(string(yamlData))
		// Dump the YAML data to a file
		file, err := os.OpenFile(cfgFile, os.O_CREATE|os.O_WRONLY|os.O_TRUNC, 0644)
		if err != nil {
			panic(err)
		}
		defer file.Close()

		_, err = file.Write(yamlData)
		if err != nil {
			fmt.Println(err)
			return err
		}

		fmt.Println("YAML data written to " + cfgFile)

		return nil
	}
	// cliconfig := dataStruct{}

	file, err := os.Open(cfgFile)
	if err != nil {
		return err
	}
	defer file.Close()

	byteSlice, err := io.ReadAll(file)
	if err != nil {
		return err
	}

	err = yaml.Unmarshal(byteSlice, &configCLI)
	if err != nil {
		return err
	}

	var token *oauth2.Token

	ctx := context.Background()
	switch configCLI.OAUTH.GrantType {
	case "authorization_code":
		cfg := oauth2.Config{
			ClientID:     configCLI.OAUTH.ClientID,
			ClientSecret: configCLI.OAUTH.ClientSecret,
			Endpoint: oauth2.Endpoint{
				TokenURL:      configCLI.OAUTH.TokenURL,
				DeviceAuthURL: configCLI.OAUTH.DeviceCodeURL,
			},
			RedirectURL: "http://localhost:8080",
			Scopes:      configCLI.OAUTH.Scopes,
		}

		response, err := cfg.DeviceAuth(ctx, oauth2.AccessTypeOffline)
		if err != nil {
			panic(err)
		}

		fmt.Printf("please enter code %s at %s\n", response.UserCode, response.VerificationURI)
		token, err = cfg.DeviceAccessToken(ctx, response, oauth2.AccessTypeOffline)
		if err != nil {
			panic(err)
		}
		// fmt.Println(token.AccessToken)
		// fmt.Println(token.RefreshToken)
		// fmt.Println(token.Expiry)
		// fmt.Println(token.TokenType)
		configCLI.OAUTH.RefreshToken = token.RefreshToken
	case "client_credentials":

		fmt.Println("Client_credentials set, I won't try to get any refresh token.")

	default:
		panic(fmt.Errorf("wrong grant type specified in the configuration. Only client_credentials and authorization_code are supported"))
	}

	valuesYAML, err := evalManifest("templates/values.yaml", configCLI)
	if err != nil {
		panic(err)
	}

	manifests := []string{
		valuesYAML,
	}

	err = os.MkdirAll(outFolder, fs.ModePerm)
	if err != nil {
		panic(err)
	}
	// Create a file and use bufio.NewWriter.
	f, err := os.Create(outFolder + "/values.yaml")
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

	fmt.Println("\n\n=== Deployment file written at:  " + outFolder + "/values.yaml ===\n\n To deploy the virtual kubelet run:\n   helm --debug upgrade --install --create-namespace -n " + configCLI.Namespace + " " + configCLI.VKName + " oci://ghcr.io/intertwin-eu/interlink-helm-chart/interlink  --values " + outFolder + "/values.yaml")

	// TODO: ilctl.sh templating
	tmpl, err := template.ParseFS(templates, "templates/interlink-install.sh")
	if err != nil {
		return err
	}

	fInterlinkScript, err := os.Create(outFolder + "/interlink-remote.sh") // in Go version older than 1.17 you can use ioutil.TempFile
	if err != nil {
		return err
	}

	// close and remove the temporary file at the end of the program
	defer fInterlinkScript.Close()
	//
	err = tmpl.Execute(fInterlinkScript, configCLI)
	if err != nil {
		return err
	}

	fmt.Println("\n\n=== Installation script for remote interLink APIs stored at: " + outFolder + "/interlink-remote.sh ===\n\n  Please execute the script on the remote server: " + configCLI.InterLinkIP + "\n\n  \"./interlink-remote.sh install\" followed by \"interlink-remote.sh start\"")

	return nil

}

func init() {
	cobra.OnInitialize(initConfig)

	rootCmd.PersistentFlags().StringVar(&cfgFile, "config", os.Getenv("HOME")+"/.interlink.yaml", "config file (default is $HOME/.interlink.yaml)")
	rootCmd.PersistentFlags().StringVar(&outFolder, "output-dir", os.Getenv("HOME")+"/.interlink", "interlink deployment manifests location (default is $HOME/.interlink)")
	rootCmd.PersistentFlags().Bool("init", false, "dump an empty configuration to get started")
	// rootCmd.AddCommand(vkCmd)
	// rootCmd.AddCommand(sdkCmd)
}

func initConfig() {
}

func main() {

	err := rootCmd.Execute()
	if err != nil {
		log.Fatal(err)
	}

}
