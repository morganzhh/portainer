package main

import (
	"log"
	"os"
	"strings"
	"time"

	"github.com/portainer/portainer/api"
	"github.com/portainer/portainer/api/bolt"
	"github.com/portainer/portainer/api/chisel"
	"github.com/portainer/portainer/api/cli"
	"github.com/portainer/portainer/api/crypto"
	"github.com/portainer/portainer/api/docker"
	"github.com/portainer/portainer/api/exec"
	"github.com/portainer/portainer/api/filesystem"
	"github.com/portainer/portainer/api/git"
	"github.com/portainer/portainer/api/http"
	"github.com/portainer/portainer/api/http/client"
	"github.com/portainer/portainer/api/internal/snapshot"
	"github.com/portainer/portainer/api/jwt"
	"github.com/portainer/portainer/api/kubernetes"
	kubecli "github.com/portainer/portainer/api/kubernetes/cli"
	"github.com/portainer/portainer/api/ldap"
	"github.com/portainer/portainer/api/libcompose"
	"github.com/portainer/portainer/api/oauth"
)

func initCLI() *portainer.CLIFlags {
	var cliService portainer.CLIService = &cli.Service{}
	flags, err := cliService.ParseFlags(portainer.APIVersion)
	if err != nil {
		log.Fatal(err)
	}

	err = cliService.ValidateFlags(flags)
	if err != nil {
		log.Fatal(err)
	}
	return flags
}

func initFileService(dataStorePath string) portainer.FileService {
	fileService, err := filesystem.NewService(dataStorePath, "")
	if err != nil {
		log.Fatal(err)
	}
	return fileService
}

func initDataStore(dataStorePath string, fileService portainer.FileService) portainer.DataStore {
	store, err := bolt.NewStore(dataStorePath, fileService)
	if err != nil {
		log.Fatal(err)
	}

	err = store.Open()
	if err != nil {
		log.Fatal(err)
	}

	err = store.Init()
	if err != nil {
		log.Fatal(err)
	}

	err = store.MigrateData()
	if err != nil {
		log.Fatal(err)
	}
	return store
}

func initComposeStackManager(dataStorePath string, reverseTunnelService portainer.ReverseTunnelService) portainer.ComposeStackManager {
	return libcompose.NewComposeStackManager(dataStorePath, reverseTunnelService)
}

func initSwarmStackManager(assetsPath string, dataStorePath string, signatureService portainer.DigitalSignatureService, fileService portainer.FileService, reverseTunnelService portainer.ReverseTunnelService) (portainer.SwarmStackManager, error) {
	return exec.NewSwarmStackManager(assetsPath, dataStorePath, signatureService, fileService, reverseTunnelService)
}

func initKubernetesDeployer(assetsPath string) portainer.KubernetesDeployer {
	return exec.NewKubernetesDeployer(assetsPath)
}

func initJWTService(dataStore portainer.DataStore) (portainer.JWTService, error) {
	settings, err := dataStore.Settings().Settings()
	if err != nil {
		return nil, err
	}

	jwtService, err := jwt.NewService(settings.UserSessionTimeout)
	if err != nil {
		return nil, err
	}
	return jwtService, nil
}

func initDigitalSignatureService() portainer.DigitalSignatureService {
	return crypto.NewECDSAService(os.Getenv("AGENT_SECRET"))
}

func initCryptoService() portainer.CryptoService {
	return &crypto.Service{}
}

func initLDAPService() portainer.LDAPService {
	return &ldap.Service{}
}

func initOAuthService() portainer.OAuthService {
	return oauth.NewService()
}

func initGitService() portainer.GitService {
	return git.NewService()
}

func initDockerClientFactory(signatureService portainer.DigitalSignatureService, reverseTunnelService portainer.ReverseTunnelService) *docker.ClientFactory {
	return docker.NewClientFactory(signatureService, reverseTunnelService)
}

func initKubernetesClientFactory(signatureService portainer.DigitalSignatureService, reverseTunnelService portainer.ReverseTunnelService, instanceID string) *kubecli.ClientFactory {
	return kubecli.NewClientFactory(signatureService, reverseTunnelService, instanceID)
}

func initSnapshotService(snapshotInterval string, dataStore portainer.DataStore, dockerClientFactory *docker.ClientFactory, kubernetesClientFactory *kubecli.ClientFactory) (portainer.SnapshotService, error) {
	dockerSnapshotter := docker.NewSnapshotter(dockerClientFactory)
	kubernetesSnapshotter := kubernetes.NewSnapshotter(kubernetesClientFactory)

	snapshotService, err := snapshot.NewService(snapshotInterval, dataStore, dockerSnapshotter, kubernetesSnapshotter)
	if err != nil {
		return nil, err
	}

	return snapshotService, nil
}

func loadEdgeJobsFromDatabase(dataStore portainer.DataStore, reverseTunnelService portainer.ReverseTunnelService) error {
	edgeJobs, err := dataStore.EdgeJob().EdgeJobs()
	if err != nil {
		return err
	}

	for _, edgeJob := range edgeJobs {
		for endpointID := range edgeJob.Endpoints {
			reverseTunnelService.AddEdgeJob(endpointID, &edgeJob)
		}
	}

	return nil
}

func initStatus(flags *portainer.CLIFlags) *portainer.Status {
	return &portainer.Status{
		Version: portainer.APIVersion,
	}
}

func updateSettingsFromFlags(dataStore portainer.DataStore, flags *portainer.CLIFlags) error {
	settings, err := dataStore.Settings().Settings()
	if err != nil {
		return err
	}

	settings.LogoURL = *flags.Logo
	settings.SnapshotInterval = *flags.SnapshotInterval
	settings.EnableEdgeComputeFeatures = *flags.EnableEdgeComputeFeatures
	settings.EnableTelemetry = true

	if len(*flags.OauthAuthorizationUrl) > 0 {
		log.Println("Initializing Oauth2 settings for the first time")
		settings.AuthenticationMethod = portainer.AuthenticationOAuth
		settings.OAuthSettings.AuthorizationURI = *flags.OauthAuthorizationUrl
		settings.OAuthSettings.AccessTokenURI = *flags.OauthTokenUrl
		settings.OAuthSettings.ClientID = *flags.OauthClientId
		settings.OAuthSettings.ClientSecret = *flags.OauthClientSecret
		settings.OAuthSettings.OAuthAutoCreateUsers = false
		settings.OAuthSettings.ResourceURI = *flags.OauthUserUrl
		settings.OAuthSettings.UserIdentifier = *flags.OauthUserKey
		settings.OAuthSettings.RedirectURI = *flags.OauthRedirectUrl
		settings.OAuthSettings.Scopes = ""
	}

	if *flags.Templates != "" {
		settings.TemplatesURL = *flags.Templates
	}

	if *flags.Labels != nil {
		settings.BlackListedLabels = *flags.Labels
	}

	return dataStore.Settings().UpdateSettings(settings)
}

func loadAndParseKeyPair(fileService portainer.FileService, signatureService portainer.DigitalSignatureService) error {
	private, public, err := fileService.LoadKeyPair()
	if err != nil {
		return err
	}
	return signatureService.ParseKeyPair(private, public)
}

func generateAndStoreKeyPair(fileService portainer.FileService, signatureService portainer.DigitalSignatureService) error {
	private, public, err := signatureService.GenerateKeyPair()
	if err != nil {
		return err
	}
	privateHeader, publicHeader := signatureService.PEMHeaders()
	return fileService.StoreKeyPair(private, public, privateHeader, publicHeader)
}

func initKeyPair(fileService portainer.FileService, signatureService portainer.DigitalSignatureService) error {
	existingKeyPair, err := fileService.KeyPairFilesExist()
	if err != nil {
		log.Fatal(err)
	}

	if existingKeyPair {
		return loadAndParseKeyPair(fileService, signatureService)
	}
	return generateAndStoreKeyPair(fileService, signatureService)
}

func createTLSSecuredEndpoint(flags *portainer.CLIFlags, dataStore portainer.DataStore, snapshotService portainer.SnapshotService) error {
	tlsConfiguration := portainer.TLSConfiguration{
		TLS:           *flags.TLS,
		TLSSkipVerify: *flags.TLSSkipVerify,
	}

	if *flags.TLS {
		tlsConfiguration.TLSCACertPath = *flags.TLSCacert
		tlsConfiguration.TLSCertPath = *flags.TLSCert
		tlsConfiguration.TLSKeyPath = *flags.TLSKey
	} else if !*flags.TLS && *flags.TLSSkipVerify {
		tlsConfiguration.TLS = true
	}

	endpointID := dataStore.Endpoint().GetNextIdentifier()
	endpoint := &portainer.Endpoint{
		ID:                 portainer.EndpointID(endpointID),
		Name:               "primary",
		URL:                *flags.EndpointURL,
		GroupID:            portainer.EndpointGroupID(1),
		Type:               portainer.DockerEnvironment,
		TLSConfig:          tlsConfiguration,
		UserAccessPolicies: portainer.UserAccessPolicies{},
		TeamAccessPolicies: portainer.TeamAccessPolicies{},
		Extensions:         []portainer.EndpointExtension{},
		TagIDs:             []portainer.TagID{},
		Status:             portainer.EndpointStatusUp,
		Snapshots:          []portainer.DockerSnapshot{},
		Kubernetes:         portainer.KubernetesDefault(),
	}

	if strings.HasPrefix(endpoint.URL, "tcp://") {
		tlsConfig, err := crypto.CreateTLSConfigurationFromDisk(tlsConfiguration.TLSCACertPath, tlsConfiguration.TLSCertPath, tlsConfiguration.TLSKeyPath, tlsConfiguration.TLSSkipVerify)
		if err != nil {
			return err
		}

		agentOnDockerEnvironment, err := client.ExecutePingOperation(endpoint.URL, tlsConfig)
		if err != nil {
			return err
		}

		if agentOnDockerEnvironment {
			endpoint.Type = portainer.AgentOnDockerEnvironment
		}
	}

	err := snapshotService.SnapshotEndpoint(endpoint)
	if err != nil {
		log.Printf("http error: endpoint snapshot error (endpoint=%s, URL=%s) (err=%s)\n", endpoint.Name, endpoint.URL, err)
	}

	return dataStore.Endpoint().CreateEndpoint(endpoint)
}

func createUnsecuredEndpoint(endpointURL string, dataStore portainer.DataStore, snapshotService portainer.SnapshotService) error {
	if strings.HasPrefix(endpointURL, "tcp://") {
		_, err := client.ExecutePingOperation(endpointURL, nil)
		if err != nil {
			return err
		}
	}

	endpointID := dataStore.Endpoint().GetNextIdentifier()
	endpoint := &portainer.Endpoint{
		ID:                 portainer.EndpointID(endpointID),
		Name:               "primary",
		URL:                endpointURL,
		GroupID:            portainer.EndpointGroupID(1),
		Type:               portainer.DockerEnvironment,
		TLSConfig:          portainer.TLSConfiguration{},
		UserAccessPolicies: portainer.UserAccessPolicies{},
		TeamAccessPolicies: portainer.TeamAccessPolicies{},
		Extensions:         []portainer.EndpointExtension{},
		TagIDs:             []portainer.TagID{},
		Status:             portainer.EndpointStatusUp,
		Snapshots:          []portainer.DockerSnapshot{},
		Kubernetes:         portainer.KubernetesDefault(),
	}

	err := snapshotService.SnapshotEndpoint(endpoint)
	if err != nil {
		log.Printf("http error: endpoint snapshot error (endpoint=%s, URL=%s) (err=%s)\n", endpoint.Name, endpoint.URL, err)
	}

	return dataStore.Endpoint().CreateEndpoint(endpoint)
}

func initEndpoint(flags *portainer.CLIFlags, dataStore portainer.DataStore, snapshotService portainer.SnapshotService) error {
	if *flags.EndpointURL == "" {
		return nil
	}

	endpoints, err := dataStore.Endpoint().Endpoints()
	if err != nil {
		return err
	}

	if len(endpoints) > 0 {
		log.Println("Instance already has defined endpoints. Skipping the endpoint defined via CLI.")
		return nil
	}

	if *flags.TLS || *flags.TLSSkipVerify {
		return createTLSSecuredEndpoint(flags, dataStore, snapshotService)
	}
	return createUnsecuredEndpoint(*flags.EndpointURL, dataStore, snapshotService)
}

func terminateIfNoAdminCreated(dataStore portainer.DataStore) {
	timer1 := time.NewTimer(5 * time.Minute)
	<-timer1.C

	users, err := dataStore.User().UsersByRole(portainer.AdministratorRole)
	if err != nil {
		log.Fatal(err)
	}

	if len(users) == 0 {
		log.Fatal("No administrator account was created after 5 min. Shutting down the Portainer instance for security reasons.")
		return
	}
}

func main() {
	flags := initCLI()

	fileService := initFileService(*flags.Data)

	dataStore := initDataStore(*flags.Data, fileService)
	defer dataStore.Close()

	jwtService, err := initJWTService(dataStore)
	if err != nil {
		log.Fatal(err)
	}

	ldapService := initLDAPService()

	oauthService := initOAuthService()

	gitService := initGitService()

	cryptoService := initCryptoService()

	digitalSignatureService := initDigitalSignatureService()

	err = initKeyPair(fileService, digitalSignatureService)
	if err != nil {
		log.Fatal(err)
	}

	reverseTunnelService := chisel.NewService(dataStore)

	instanceID, err := dataStore.Version().InstanceID()
	if err != nil {
		log.Fatal(err)
	}

	dockerClientFactory := initDockerClientFactory(digitalSignatureService, reverseTunnelService)
	kubernetesClientFactory := initKubernetesClientFactory(digitalSignatureService, reverseTunnelService, instanceID)

	snapshotService, err := initSnapshotService(*flags.SnapshotInterval, dataStore, dockerClientFactory, kubernetesClientFactory)
	if err != nil {
		log.Fatal(err)
	}
	snapshotService.Start()

	swarmStackManager, err := initSwarmStackManager(*flags.Assets, *flags.Data, digitalSignatureService, fileService, reverseTunnelService)
	if err != nil {
		log.Fatal(err)
	}

	composeStackManager := initComposeStackManager(*flags.Data, reverseTunnelService)

	kubernetesDeployer := initKubernetesDeployer(*flags.Assets)

	if dataStore.IsNew() {
		err = updateSettingsFromFlags(dataStore, flags)
		if err != nil {
			log.Fatal(err)
		}
	}

	err = loadEdgeJobsFromDatabase(dataStore, reverseTunnelService)
	if err != nil {
		log.Fatal(err)
	}

	applicationStatus := initStatus(flags)

	err = initEndpoint(flags, dataStore, snapshotService)
	if err != nil {
		log.Fatal(err)
	}

	adminPasswordHash := ""
	if *flags.AdminPasswordFile != "" {
		content, err := fileService.GetFileContent(*flags.AdminPasswordFile)
		if err != nil {
			log.Fatal(err)
		}
		adminPasswordHash, err = cryptoService.Hash(strings.TrimSuffix(string(content), "\n"))
		if err != nil {
			log.Fatal(err)
		}
	} else if *flags.AdminPassword != "" {
		adminPasswordHash = *flags.AdminPassword
	}

	if adminPasswordHash != "" {
		users, err := dataStore.User().UsersByRole(portainer.AdministratorRole)
		if err != nil {
			log.Fatal(err)
		}

		if len(users) == 0 {
			log.Println("Created admin user with the given password.")
			user := &portainer.User{
				Username: "admin",
				Role:     portainer.AdministratorRole,
				Password: adminPasswordHash,
			}
			err := dataStore.User().CreateUser(user)
			if err != nil {
				log.Fatal(err)
			}
		} else {
			log.Println("Instance already has an administrator user defined. Skipping admin password related flags.")
		}
	}

	go terminateIfNoAdminCreated(dataStore)

	err = reverseTunnelService.StartTunnelServer(*flags.TunnelAddr, *flags.TunnelPort, snapshotService)
	if err != nil {
		log.Fatal(err)
	}

	var server portainer.Server = &http.Server{
		ReverseTunnelService:    reverseTunnelService,
		Status:                  applicationStatus,
		BindAddress:             *flags.Addr,
		AssetsPath:              *flags.Assets,
		DataStore:               dataStore,
		SwarmStackManager:       swarmStackManager,
		ComposeStackManager:     composeStackManager,
		KubernetesDeployer:      kubernetesDeployer,
		CryptoService:           cryptoService,
		JWTService:              jwtService,
		FileService:             fileService,
		LDAPService:             ldapService,
		OAuthService:            oauthService,
		GitService:              gitService,
		SignatureService:        digitalSignatureService,
		SnapshotService:         snapshotService,
		SSL:                     *flags.SSL,
		SSLCert:                 *flags.SSLCert,
		SSLKey:                  *flags.SSLKey,
		DockerClientFactory:     dockerClientFactory,
		KubernetesClientFactory: kubernetesClientFactory,
	}

	log.Printf("Starting Portainer %s on %s", portainer.APIVersion, *flags.Addr)
	err = server.Start()
	if err != nil {
		log.Fatal(err)
	}
}
