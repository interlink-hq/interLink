package main

import (
	"bufio"
	"context"
	"embed"
	"fmt"
	"io"
	"os"
	"text/template"

	"github.com/spf13/cobra"
	"golang.org/x/oauth2"
	"gopkg.in/yaml.v3"
)

var (
	// Used for flags.
	cfgFile     string
	outFile     string
	userLicense string

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
	RefreshToken  string   `yaml:"refresh_token,omitempty"`
	Scopes        []string `yaml:"scopes"`
	TokenURL      string   `yaml:"token_url"`
	DeviceCodeURL string   `yaml:"device_code_url"`
	ClientID      string   `yaml:"client_id"`
	ClientSecret  string   `yaml:"client_secret"`
}

type dataStruct struct {
	OAUTH         oauthStruct `yaml:"oauth,omitempty"`
	InterLinkURL  string      `yaml:"interlink_url"`
	InterLinkPort int         `yaml:"interlink_port"`
	VKName        string      `yaml:"kubelet_node_name"`
	Namespace     string      `yaml:"kubernetes_namespace,omitempty"`
	VKLimits      Resources   `yaml:"node_limits"`
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

func root(cmd *cobra.Command, args []string) error {
	var configCLI dataStruct

	onlyInit, err := cmd.Flags().GetBool("init")
	if err != nil {
		return err
	}

	if onlyInit {
		dumpConfig := dataStruct{
			VKName:    "my_VK_Node",
			Namespace: "interlink",
			VKLimits: Resources{
				CPU:    "10",
				Memory: "256Gi",
				Pods:   "10",
			},
			InterLinkURL:  "https://example.com",
			InterLinkPort: 8443,
			OAUTH: oauthStruct{
				ClientID:      "",
				ClientSecret:  "",
				Scopes:        []string{""},
				TokenURL:      "",
				DeviceCodeURL: "",
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
	//cliconfig := dataStruct{}

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

	ctx := context.Background()
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
	token, err := cfg.DeviceAccessToken(ctx, response, oauth2.AccessTypeOffline)
	if err != nil {
		panic(err)
	}
	fmt.Println(token.AccessToken)
	fmt.Println(token.RefreshToken)
	fmt.Println(token.Expiry)
	fmt.Println(token.TokenType)

	configCLI.OAUTH.RefreshToken = token.RefreshToken

	namespaceYAML, err := evalManifest("templates/namespace.yaml", configCLI)
	if err != nil {
		panic(err)
	}

	deploymentYAML, err := evalManifest("templates/deployment.yaml", configCLI)
	if err != nil {
		panic(err)
	}

	configYAML, err := evalManifest("templates/configs.yaml", configCLI)
	if err != nil {
		panic(err)
	}

	serviceaccountYAML, err := evalManifest("templates/service-account.yaml", configCLI)
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
	f, err := os.Create(outFile)
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

	fmt.Println("Deployment file written at: " + outFile)

	// TODO: ilctl.sh templating

	return nil

}

func init() {
	cobra.OnInitialize(initConfig)

	rootCmd.PersistentFlags().StringVar(&cfgFile, "config", os.Getenv("HOME")+"/.interlink.yaml", "config file (default is $HOME/.interlink.yaml)")
	rootCmd.PersistentFlags().StringVar(&outFile, "output", os.Getenv("HOME")+"/.interlink-deployment.yaml", "interlink deployment manifest location file (default is $HOME/.interlink-deployment.yaml)")
	rootCmd.PersistentFlags().Bool("init", false, "dump an empty configuration to get started")
	// rootCmd.AddCommand(vkCmd)
	// rootCmd.AddCommand(sdkCmd)
}

func initConfig() {
}

func main() {

	rootCmd.Execute()

}
