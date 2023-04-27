package main

import (
	"crypto/tls"
	"flag"
	"net/http"
	"os"
	"runtime"
	"strconv"
	"time"

	"github.com/RocksLabs/kvrocks_exporter/exporter"
	"github.com/prometheus/client_golang/prometheus"
	log "github.com/sirupsen/logrus"
)

var (
	/*
		BuildVersion, BuildDate, BuildCommitSha are filled in by the build script
	*/
	BuildVersion   = "<<< filled in by build >>>"
	BuildDate      = "<<< filled in by build >>>"
	BuildCommitSha = "<<< filled in by build >>>"
)

func getEnv(key string, defaultVal string) string {
	if envVal, ok := os.LookupEnv(key); ok {
		return envVal
	}
	return defaultVal
}

func getEnvBool(key string, defaultVal bool) bool {
	if envVal, ok := os.LookupEnv(key); ok {
		envBool, err := strconv.ParseBool(envVal)
		if err == nil {
			return envBool
		}
	}
	return defaultVal
}

func main() {
	var (
		redisAddr           = flag.String("kvrocks.addr", getEnv("KVROCKS_ADDR", "kvrocks://localhost:6666"), "Address of the Kvrocks instance to scrape")
		kvrocksPwd          = flag.String("kvrocks.password", getEnv("KVROCKS_PASSWORD", ""), "Password of the Kvrocks instance to scrape")
		kvrocksPwdFile      = flag.String("kvrocks.password-file", getEnv("KVROCKS_PASSWORD_FILE", ""), "Password file of the Kvrocks instance to scrape")
		namespace           = flag.String("namespace", getEnv("KVROCKS_EXPORTER_NAMESPACE", "kvrocks"), "Namespace for metrics")
		listenAddress       = flag.String("web.listen-address", getEnv("KVROCKS_EXPORTER_WEB_LISTEN_ADDRESS", ":9121"), "Address to listen on for web interface and telemetry.")
		metricPath          = flag.String("web.telemetry-path", getEnv("KVROCKS_EXPORTER_WEB_TELEMETRY_PATH", "/metrics"), "Path under which to expose metrics.")
		logFormat           = flag.String("log-format", getEnv("KVROCKS_EXPORTER_LOG_FORMAT", "txt"), "Log format, valid options are txt and json")
		configCommand       = flag.String("config-command", getEnv("KVROCKS_EXPORTER_CONFIG_COMMAND", "CONFIG"), "What to use for the CONFIG command")
		connectionTimeout   = flag.String("connection-timeout", getEnv("KVROCKS_EXPORTER_CONNECTION_TIMEOUT", "15s"), "Timeout for connection to Kvrocks instance")
		tlsClientKeyFile    = flag.String("tls-client-key-file", getEnv("KVROCKS_EXPORTER_TLS_CLIENT_KEY_FILE", ""), "Name of the client key file (including full path) if the server requires TLS client authentication")
		tlsClientCertFile   = flag.String("tls-client-cert-file", getEnv("KVROCKS_EXPORTER_TLS_CLIENT_CERT_FILE", ""), "Name of the client certificate file (including full path) if the server requires TLS client authentication")
		tlsCaCertFile       = flag.String("tls-ca-cert-file", getEnv("KVROCKS_EXPORTER_TLS_CA_CERT_FILE", ""), "Name of the CA certificate file (including full path) if the server requires TLS client authentication")
		tlsServerKeyFile    = flag.String("tls-server-key-file", getEnv("KVROCKS_EXPORTER_TLS_SERVER_KEY_FILE", ""), "Name of the server key file (including full path) if the web interface and telemetry should use TLS")
		tlsServerCertFile   = flag.String("tls-server-cert-file", getEnv("KVROCKS_EXPORTER_TLS_SERVER_CERT_FILE", ""), "Name of the server certificate file (including full path) if the web interface and telemetry should use TLS")
		isDebug             = flag.Bool("debug", getEnvBool("KVROCKS_EXPORTER_DEBUG", false), "Output verbose debug information")
		setClientName       = flag.Bool("set-client-name", getEnvBool("KVROCKS_EXPORTER_SET_CLIENT_NAME", true), "Whether to set client name to kvrocks_exporter")
		isCluster           = flag.Bool("is-cluster", getEnvBool("KVROCKS_EXPORTER_IS_CLUSTER", false), "Whether this is a Kvrocks cluster (Enable this if you need to fetch key level data on a Kvrocks Cluster).")
		exportClientPort    = flag.Bool("export-client-port", getEnvBool("KVROCKS_EXPORTER_EXPORT_CLIENT_PORT", false), "Whether to include the client's port when exporting the client list. Warning: including the port increases the number of metrics generated and will make your Prometheus server take up more memory")
		showVersion         = flag.Bool("version", false, "Show version information and exit")
		pingOnConnect       = flag.Bool("ping-on-connect", getEnvBool("KVROCKS_EXPORTER_PING_ON_CONNECT", false), "Whether to ping the Kvrocks instance after connecting")
		inclSystemMetrics   = flag.Bool("include-system-metrics", getEnvBool("KVROCKS_EXPORTER_INCL_SYSTEM_METRICS", false), "Whether to include system metrics like e.g. kvrocks_total_system_memory_bytes")
		skipTLSVerification = flag.Bool("skip-tls-verification", getEnvBool("KVROCKS_EXPORTER_SKIP_TLS_VERIFICATION", false), "Whether to to skip TLS verification")
	)
	flag.Parse()

	switch *logFormat {
	case "json":
		log.SetFormatter(&log.JSONFormatter{})
	default:
		log.SetFormatter(&log.TextFormatter{})
	}
	log.Printf("Redis Metrics Exporter %s    build date: %s    sha1: %s    Go: %s    GOOS: %s    GOARCH: %s",
		BuildVersion, BuildDate, BuildCommitSha,
		runtime.Version(),
		runtime.GOOS,
		runtime.GOARCH,
	)
	if *isDebug {
		log.SetLevel(log.DebugLevel)
		log.Debugln("Enabling debug output")
	} else {
		log.SetLevel(log.InfoLevel)
	}

	if *showVersion {
		return
	}

	to, err := time.ParseDuration(*connectionTimeout)
	if err != nil {
		log.Fatalf("Couldn't parse connection timeout duration, err: %s", err)
	}

	passwordMap := make(map[string]string)
	if *kvrocksPwd == "" && *kvrocksPwdFile != "" {
		passwordMap, err = exporter.LoadPwdFile(*kvrocksPwdFile)
		if err != nil {
			log.Fatalf("Error loading kvrocks passwords from file %s, err: %s", *kvrocksPwdFile, err)
		}
	}

	registry := prometheus.NewRegistry()
	registry = prometheus.DefaultRegisterer.(*prometheus.Registry)

	exp, err := exporter.NewKvrocksExporter(
		*redisAddr,
		exporter.Options{
			Password:              *kvrocksPwd,
			PasswordMap:           passwordMap,
			Namespace:             *namespace,
			ConfigCommandName:     *configCommand,
			InclSystemMetrics:     *inclSystemMetrics,
			SetClientName:         *setClientName,
			IsCluster:             *isCluster,
			ExportClientsInclPort: *exportClientPort,
			SkipTLSVerification:   *skipTLSVerification,
			ClientCertFile:        *tlsClientCertFile,
			ClientKeyFile:         *tlsClientKeyFile,
			CaCertFile:            *tlsCaCertFile,
			ConnectionTimeouts:    to,
			MetricsPath:           *metricPath,
			PingOnConnect:         *pingOnConnect,
			Registry:              registry,
			BuildInfo: exporter.BuildInfo{
				Version:   BuildVersion,
				CommitSha: BuildCommitSha,
				Date:      BuildDate,
			},
		},
	)
	if err != nil {
		log.Fatal(err)
	}

	// Verify that initial client keypair and CA are accepted
	if (*tlsClientCertFile != "") != (*tlsClientKeyFile != "") {
		log.Fatal("TLS client key file and cert file should both be present")
	}
	_, err = exp.CreateClientTLSConfig()
	if err != nil {
		log.Fatal(err)
	}

	log.Infof("Providing metrics at %s%s", *listenAddress, *metricPath)
	log.Debugf("Configured redis addr: %#v", *redisAddr)
	if *tlsServerCertFile != "" && *tlsServerKeyFile != "" {
		log.Debugf("Bind as TLS using cert %s and key %s", *tlsServerCertFile, *tlsServerKeyFile)

		// Verify that the initial key pair is accepted
		_, err := exporter.LoadKeyPair(*tlsServerCertFile, *tlsServerKeyFile)
		if err != nil {
			log.Fatalf("Couldn't load TLS server key pair, err: %s", err)
		}
		server := &http.Server{
			Addr:      *listenAddress,
			TLSConfig: &tls.Config{GetCertificate: exporter.GetServerCertificateFunc(*tlsServerCertFile, *tlsServerKeyFile)},
			Handler:   exp}
		log.Fatal(server.ListenAndServeTLS("", ""))
	} else {
		log.Fatal(http.ListenAndServe(*listenAddress, exp))
	}
}
