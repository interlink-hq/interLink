// Implements the interLink installer CLI tool.
//
// The interLink installer automates the deployment of interLink components
// across different environments. It generates configuration files, deployment
// manifests, and installation scripts needed to set up interLink in various
// deployment scenarios (Edge-node, In-cluster, Tunneled).
//
// The installer creates Helm chart values for Kubernetes deployment, 
// and generates installation scripts for remote interLink APIs.
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
	// cfgFile is the path to the configuration file
	cfgFile string
	
	// outFolder is the directory where deployment manifests will be stored
	outFolder string

	// rootCmd is the main command for the interLink installer CLI
	rootCmd = &cobra.Command{
		Use:   "interlink-installer",
		Short: "CLI to manage interLink deployment",
		Long:  `interLink cloud tools allows to extend kubernetes cluster over any remote resource`,
		RunE:  root,
	}
	
	//go:embed templates
	// templates contains the embedded template files used for generating
	// deployment manifests and installation scripts
	templates embed.FS
)

// Resources defines the resource limits for the virtual kubelet node.
// These limits determine the maximum resources that can be allocated
// to pods running on the virtual node.
type Resources struct {
	// CPU is the maximum CPU cores available on the virtual node
	CPU    string `yaml:"cpu"`
	
	// Memory is the maximum memory in GiB available on the virtual node
	Memory string `yaml:"memory"`
	
	// Pods is the maximum number of pods that can run on the virtual node
	Pods   string `yaml:"pods"`
}

// oauthStruct defines the OAuth configuration for authentication.
// It supports both OIDC and GitHub authentication providers.
type oauthStruct struct {
	// Provider specifies the OAuth provider (e.g., "oidc", "github")
	Provider      string   `yaml:"provider"`
	
	// GrantType specifies the OAuth grant type (e.g., "authorization_code", "client_credentials")
	GrantType     string   `default:"authorization_code" yaml:"grant_type"`
	
	// Issuer is the OIDC issuer URL (only used with OIDC provider)
	Issuer        string   `yaml:"issuer,omitempty"`
	
	// RefreshToken is the OAuth refresh token obtained during authentication
	RefreshToken  string   `yaml:"refresh_token,omitempty"`
	
	// Audience is the intended audience for the token (only used with OIDC provider)
	Audience      string   `yaml:"audience,omitempty"`
	
	// Group is the required group membership for authentication
	Group         string   `yaml:"group,omitempty"`
	
	// GroupClaim is the claim name in the token that contains group information
	GroupClaim    string   `default:"groups" yaml:"group_claim"`
	
	// Scopes are the OAuth scopes requested during authentication
	Scopes        []string `yaml:"scopes"`
	
	// GitHUBUser is the GitHub username (only used with GitHub provider)
	GitHUBUser    string   `yaml:"github_user"`
	
	// TokenURL is the OAuth token endpoint URL
	TokenURL      string   `yaml:"token_url"`
	
	// DeviceCodeURL is the OAuth device code endpoint URL
	DeviceCodeURL string   `yaml:"device_code_url"`
	
	// ClientID is the OAuth client ID
	ClientID      string   `yaml:"client_id"`
	
	// ClientSecret is the OAuth client secret
	ClientSecret  string   `yaml:"client_secret"`
}

// dataStruct is the main configuration structure for interLink deployment.
// It contains all the information needed to generate deployment manifests
// and installation scripts.
//
// TODO: insert in-cluster and socket option e.g. --> no need OAUTH
type dataStruct struct {
	// InterLinkIP is the IP address where the interLink API will be exposed
	InterLinkIP      string      `yaml:"interlink_ip"`
	
	// InterLinkPort is the port where the interLink API will be exposed
	InterLinkPort    int         `yaml:"interlink_port"`
	
	// InterLinkVersion is the version of interLink to deploy
	InterLinkVersion string      `yaml:"interlink_version"`
	
	// VKName is the name of the virtual kubelet node
	VKName           string      `yaml:"kubelet_node_name"`
	
	// Namespace is the Kubernetes namespace where interLink will be deployed
	Namespace        string      `yaml:"kubernetes_namespace,omitempty"`
	
	// VKLimits defines the resource limits for the virtual kubelet node
	VKLimits         Resources   `yaml:"node_limits"`
	
	// OAUTH contains the OAuth configuration for authentication
	OAUTH            oauthStruct `yaml:"oauth,omitempty"`
	
	// HTTPInsecure determines whether to allow insecure HTTP connections
	HTTPInsecure     bool        `default:"true" yaml:"insecure_http"`
}

// evalManifest evaluates a template file using the provided configuration data.
// It parses the template from the embedded filesystem, executes it with the
// configuration data, and returns the rendered template as a string.
//
// Parameters:
//   - path: The path to the template file within the embedded filesystem
//   - dataStruct: The configuration data to use for template rendering
//
// Returns:
//   - string: The rendered template as a string
//   - error: An error if template parsing, execution, or reading fails
func evalManifest(path string, dataStruct dataStruct) (string, error) {
	// Parse the template from the embedded filesystem
	tmpl, err := template.ParseFS(templates, path)
	if err != nil {
		return "", err
	}

	// Create a temporary file to store the rendered template
	fDeploy, err := os.CreateTemp("", "tmpfile-") // in Go version older than 1.17 you can use ioutil.TempFile
	if err != nil {
		return "", err
	}

	// Close and remove the temporary file at the end of the function
	defer fDeploy.Close()
	defer os.Remove(fDeploy.Name())

	// Execute the template with the configuration data
	err = tmpl.Execute(fDeploy, dataStruct)
	if err != nil {
		return "", err
	}

	// Read the rendered template from the temporary file
	deploymentYAML, err := os.ReadFile(fDeploy.Name())
	if err != nil {
		return "", err
	}

	return string(deploymentYAML), nil
}

// root is the main command execution function for the interLink installer.
// It handles initialization of configuration, OAuth authentication, and
// generation of deployment manifests and installation scripts.
//
// Parameters:
//   - cmd: The Cobra command being executed
//   - _: Unused parameter for command arguments
//
// Returns:
//   - error: An error if any operation fails
func root(cmd *cobra.Command, _ []string) error {
	var configCLI dataStruct

	// Check if the --init flag is set
	onlyInit, err := cmd.Flags().GetBool("init")
	if err != nil {
		return err
	}

	// If --init flag is set, create a default configuration file
	if onlyInit {
		// Check if the configuration file already exists
		if _, err = os.Stat(cfgFile); err == nil {
			return fmt.Errorf("File config file exists. Please remove it before trying init again: %w", err)
		}

		// Create a default configuration with placeholder values
		dumpConfig := dataStruct{
			VKName:    "my-vk-node",
			Namespace: "interlink",
			VKLimits: Resources{
				CPU:    "10",
				Memory: "256",
				Pods:   "10",
			},
			InterLinkIP:      "PUBLIC_IP_HERE",
			InterLinkPort:    -1,
			InterLinkVersion: "0.3.3",
			OAUTH: oauthStruct{
				ClientID:      "OIDC_CLIENT_ID_HERE",
				ClientSecret:  "OIDC_CLIENT_SECRET_HERE",
				Scopes:        []string{"openid", "email", "offline_access", "profile"},
				TokenURL:      "https://my_oidc_idp.com/token",
				DeviceCodeURL: "https://my_oidc_idp/auth/device",
				Provider:      "oidc",
				Issuer:        "https://my_oidc_idp.com/",
			},
			HTTPInsecure: true,
		}

		// Marshal the configuration to YAML
		yamlData, err := yaml.Marshal(dumpConfig)
		if err != nil {
			fmt.Println(err)
			return err
		}

		// Print the YAML configuration to stdout
		fmt.Println(string(yamlData))
		
		// Write the YAML configuration to the specified file
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
	// Read and parse the configuration file
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

	// Handle OAuth authentication based on the grant type
	var token *oauth2.Token
	ctx := context.Background()
	
	switch configCLI.OAUTH.GrantType {
	case "authorization_code":
		// Set up OAuth configuration for device authorization flow
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

		// Initiate device authorization flow
		response, err := cfg.DeviceAuth(ctx, oauth2.AccessTypeOffline)
		if err != nil {
			panic(err)
		}

		// Prompt the user to enter the code at the verification URI
		fmt.Printf("please enter code %s at %s\n", response.UserCode, response.VerificationURI)
		
		// Exchange the device code for an access token and refresh token
		token, err = cfg.DeviceAccessToken(ctx, response, oauth2.AccessTypeOffline)
		if err != nil {
			panic(err)
		}
		
		// Store the refresh token in the configuration
		// The refresh token is used for obtaining new access tokens without user interaction
		configCLI.OAUTH.RefreshToken = token.RefreshToken
	case "client_credentials":
		// Client credentials grant type doesn't use refresh tokens
		fmt.Println("Client_credentials set, I won't try to get any refresh token.")

	default:
		// Unsupported grant type
		panic(fmt.Errorf("wrong grant type specified in the configuration. Only client_credentials and authorization_code are supported"))
	}

	// Generate the values.yaml manifest from the template
	valuesYAML, err := evalManifest("templates/values.yaml", configCLI)
	if err != nil {
		panic(err)
	}

	// Collect all manifests to be written
	manifests := []string{
		valuesYAML,
	}

	// Create the output directory if it doesn't exist
	err = os.MkdirAll(outFolder, fs.ModePerm)
	if err != nil {
		panic(err)
	}
	
	// Create the values.yaml file and use bufio.NewWriter for efficient writing
	f, err := os.Create(outFolder + "/values.yaml")
	if err != nil {
		panic(err)
	}
	defer f.Close()
	w := bufio.NewWriter(f)

	// Write each manifest to the file, separated by YAML document separators
	for _, mnfst := range manifests {
		fmt.Fprint(w, mnfst)
		fmt.Fprint(w, "\n---\n")
	}

	// Flush the writer to ensure all data is written to the file
	w.Flush()

	// Print information about the generated values.yaml file and how to use it
	fmt.Println("\n\n=== Deployment file written at:  " + outFolder + "/values.yaml ===\n\n To deploy the virtual kubelet run:\n   helm --debug upgrade --install --create-namespace -n " + configCLI.Namespace + " " + configCLI.VKName + " oci://ghcr.io/intertwin-eu/interlink-helm-chart/interlink  --values " + outFolder + "/values.yaml")

	// Generate the installation script for remote interLink APIs
	// TODO: ilctl.sh templating
	tmpl, err := template.ParseFS(templates, "templates/interlink-install.sh")
	if err != nil {
		return err
	}

	// Create the installation script file
	fInterlinkScript, err := os.Create(outFolder + "/interlink-remote.sh")
	if err != nil {
		return err
	}

	// Close the file when the function returns
	defer fInterlinkScript.Close()
	
	// Execute the template with the configuration data
	err = tmpl.Execute(fInterlinkScript, configCLI)
	if err != nil {
		return err
	}

	// Print information about the generated installation script and how to use it
	fmt.Println("\n\n=== Installation script for remote interLink APIs stored at: " + outFolder + "/interlink-remote.sh ===\n\n  Please execute the script on the remote server: " + configCLI.InterLinkIP + "\n\n  \"./interlink-remote.sh install\" followed by \"interlink-remote.sh start\"")

	return nil
}

// init initializes the command-line flags and configuration.
// It is called automatically when the package is initialized.
func init() {
	cobra.OnInitialize(initConfig)

	// Define command-line flags
	rootCmd.PersistentFlags().StringVar(&cfgFile, "config", os.Getenv("HOME")+"/.interlink.yaml", "config file (default is $HOME/.interlink.yaml)")
	rootCmd.PersistentFlags().StringVar(&outFolder, "output-dir", os.Getenv("HOME")+"/.interlink/manifests", "interlink deployment manifests location (default is $HOME/.interlink/manifests)")
	rootCmd.PersistentFlags().Bool("init", false, "dump an empty configuration to get started")
	
	// Commented out commands that might be added in the future
	// rootCmd.AddCommand(vkCmd)
	// rootCmd.AddCommand(sdkCmd)
}

// initConfig is called during initialization to set up configuration.
// Currently empty, but can be extended to support additional configuration options.
func initConfig() {
	// This function is currently empty but is called by Cobra during initialization.
	// It can be used to read in config files or set up environment variables.
}

// main is the entry point for the interLink installer CLI.
// It executes the root command and handles any errors.
func main() {
	// Execute the root command
	err := rootCmd.Execute()
	if err != nil {
		log.Fatal(err)
	}
}
