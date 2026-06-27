package start

import (
	"context"
	"errors"
	"fmt"
	"math"
	"net"
	"net/url"
	"os"
	"strings"
	"time"

	"github.com/redis/go-redis/v9"
	"github.com/redis/go-redis/v9/maintnotifications"
	"github.com/urfave/cli/v3"
	"go.uber.org/zap"

	"gh.tarampamp.am/webhook-tester/v2/internal/cli/start/healthcheck"
	"gh.tarampamp.am/webhook-tester/v2/internal/config"
	"gh.tarampamp.am/webhook-tester/v2/internal/encoding"
	appHttp "gh.tarampamp.am/webhook-tester/v2/internal/http"
	"gh.tarampamp.am/webhook-tester/v2/internal/identifiers"
	"gh.tarampamp.am/webhook-tester/v2/internal/logger"
	"gh.tarampamp.am/webhook-tester/v2/internal/pubsub"
	"gh.tarampamp.am/webhook-tester/v2/internal/storage"
	"gh.tarampamp.am/webhook-tester/v2/internal/storage/hotindex"
	"gh.tarampamp.am/webhook-tester/v2/internal/tunnel"
	"gh.tarampamp.am/webhook-tester/v2/internal/version"
)

type (
	command struct {
		c *cli.Command

		options struct {
			addr string // IP (v4 or v6) address to listen on
			http struct {
				tcpPort uint16 // TCP port number for HTTP server
			}
			timeouts struct {
				httpRead, httpWrite, httpIdle time.Duration // timeouts for HTTP(s) servers
				shutdown                      time.Duration // maximum amount of time to wait for the server to stop
			}
			storage struct {
				driver      string        // storage driver
				sessionTTL  time.Duration // session TTL
				maxRequests uint16        // maximal number of requests
				fsDir       string        // path to the directory for local fs storage
				sqlitePath  string        // path to the SQLite database file
			}
			auth struct {
				token string // shared dashboard/API auth token (empty = disabled)
			}
			identifiers struct {
				keys    []string // JSON body / query-param field names to index
				headers []string // HTTP header names to index
			}
			response struct {
				scriptTimeout time.Duration // max execution time for a response script
			}
			hotIndex struct {
				window time.Duration // retention window for the in-memory hot index
			}
			pubSub struct {
				driver string // Pub/Sub driver
			}
			tunnel struct {
				driver string // tunnel driver
			}
			ngrok struct {
				authToken string // ngrok authentication token
			}
			redis struct {
				dsn string // redis-like server DSN
			}
			frontend struct {
				useLive bool // false to use embedded frontend, true to use live (local)
			}
			maxRequestPayloadSize uint32
			autoCreateSessions    bool
			publicURLRoot         string // public URL root override
		}
	}
)

const (
	pubSubDriverMemory, pubSubDriverRedis = "memory", "redis"

	storageDriverMemory, storageDriverRedis = "memory", "redis"
	storageDriverFS, storageDriverSQLite    = "fs", "sqlite"

	tunnelDriverNgrok = "ngrok"

	// default identifier keys extracted from request bodies / query params.
	defaultIdentifierKeyTracking  = "trackingId"
	defaultIdentifierKeyReference = "referenceId"
)

// NewCommand creates new `start` command.
func NewCommand(log *zap.Logger, defaultHttpPort uint16) *cli.Command { //nolint:funlen
	var cmd command

	const httpCategory, tunnelCategory = "HTTP", "TUNNEL"

	var (
		httpAddrFlag = cli.StringFlag{
			Name:     "addr",
			Category: httpCategory,
			Usage:    "IP (v4 or v6) address to listen on (0.0.0.0 to bind to all interfaces)",
			Value:    "0.0.0.0",
			Sources:  cli.EnvVars("SERVER_ADDR", "LISTEN_ADDR"),
			OnlyOnce: true,
			Config:   cli.StringConfig{TrimSpace: true},
			Validator: func(ip string) error {
				if ip == "" {
					return fmt.Errorf("missing IP address")
				}

				if net.ParseIP(ip) == nil {
					return fmt.Errorf("wrong IP address [%s] for listening", ip)
				}

				return nil
			},
		}
		httpPortFlag = cli.UintFlag{
			Name:     "port",
			Category: httpCategory,
			Usage:    "HTTP server port",
			Value:    uint(defaultHttpPort),
			Sources:  cli.EnvVars("HTTP_PORT"),
			OnlyOnce: true,
			Validator: func(port uint) error {
				if port == 0 || port > math.MaxUint16 {
					return fmt.Errorf("wrong TCP port number [%d]", port)
				}

				return nil
			},
		}
		httpReadTimeoutFlag = cli.DurationFlag{
			Name:      "read-timeout",
			Category:  httpCategory,
			Usage:     "maximum duration for reading the entire request, including the body (zero = no timeout)",
			Value:     time.Second * 60, //nolint:mnd
			Sources:   cli.EnvVars("HTTP_READ_TIMEOUT"),
			OnlyOnce:  true,
			Validator: validateDuration("read timeout", time.Millisecond, time.Hour),
		}
		httpWriteTimeoutFlag = cli.DurationFlag{
			Name:      "write-timeout",
			Category:  httpCategory,
			Usage:     "maximum duration before timing out writes of the response (zero = no timeout)",
			Value:     time.Second * 60, //nolint:mnd
			Sources:   cli.EnvVars("HTTP_WRITE_TIMEOUT"),
			OnlyOnce:  true,
			Validator: validateDuration("write timeout", time.Millisecond, time.Hour),
		}
		httpIdleTimeoutFlag = cli.DurationFlag{
			Name:      "idle-timeout",
			Category:  httpCategory,
			Usage:     "maximum amount of time to wait for the next request (keep-alive, zero = no timeout)",
			Value:     time.Second * 60, //nolint:mnd
			Sources:   cli.EnvVars("HTTP_IDLE_TIMEOUT"),
			OnlyOnce:  true,
			Validator: validateDuration("idle timeout", time.Millisecond, time.Hour),
		}
		storageDriverFlag = cli.StringFlag{
			Name:  "storage-driver",
			Value: storageDriverSQLite,
			Usage: "storage driver (" + strings.Join([]string{
				storageDriverSQLite,
				storageDriverMemory,
				storageDriverRedis,
				storageDriverFS,
			}, "/") + ")",
			Sources:  cli.EnvVars("STORAGE_DRIVER"),
			OnlyOnce: true,
			Config:   cli.StringConfig{TrimSpace: true},
			Validator: func(s string) error {
				switch s {
				case storageDriverSQLite, storageDriverMemory, storageDriverRedis, storageDriverFS:
					return nil
				default:
					return fmt.Errorf("wrong storage driver [%s]", s)
				}
			},
		}
		sqlitePathFlag = cli.StringFlag{
			Name:     "sqlite-path",
			Usage:    "path to the SQLite database file (created if absent; used by the sqlite storage driver)",
			Value:    "./webhook-tester.db",
			Sources:  cli.EnvVars("SQLITE_PATH"),
			OnlyOnce: true,
			Config:   cli.StringConfig{TrimSpace: true},
		}
		authTokenFlag = cli.StringFlag{
			Name:     "auth-token",
			Usage:    "shared token protecting the dashboard and /api endpoints (empty disables auth)",
			Sources:  cli.EnvVars("AUTH_TOKEN"),
			OnlyOnce: true,
			Config:   cli.StringConfig{TrimSpace: true},
		}
		identifierKeysFlag = cli.StringSliceFlag{
			Name:    "identifier-keys",
			Usage:   "JSON body field and query-param names to extract as searchable identifiers",
			Value:   []string{defaultIdentifierKeyTracking, defaultIdentifierKeyReference},
			Sources: cli.EnvVars("IDENTIFIER_KEYS"),
		}
		identifierHeadersFlag = cli.StringSliceFlag{
			Name:    "identifier-headers",
			Usage:   "HTTP header names to extract as searchable identifiers",
			Sources: cli.EnvVars("IDENTIFIER_HEADERS"),
		}
		responseScriptTimeoutFlag = cli.DurationFlag{
			Name:      "response-script-timeout",
			Usage:     "maximum execution time for a session response (go-template) script",
			Value:     time.Second,
			Sources:   cli.EnvVars("RESPONSE_SCRIPT_TIMEOUT"),
			OnlyOnce:  true,
			Validator: validateDuration("response script timeout", time.Millisecond, time.Minute),
		}
		hotIndexWindowFlag = cli.DurationFlag{
			Name:      "hot-index-window",
			Usage:     "retention window for the in-memory identifier hot index (search fast path)",
			Value:     time.Hour * 24 * 7, //nolint:mnd // 7 days
			Sources:   cli.EnvVars("HOT_INDEX_WINDOW"),
			OnlyOnce:  true,
			Validator: validateDuration("hot index window", time.Minute, time.Hour*24*31), //nolint:mnd
		}
		storageSessionTTLFlag = cli.DurationFlag{
			Name:      "session-ttl",
			Usage:     "session TTL (time-to-live, lifetime)",
			Value:     time.Hour * 24 * 7, //nolint:mnd
			Sources:   cli.EnvVars("SESSION_TTL"),
			OnlyOnce:  true,
			Validator: validateDuration("session TTL", time.Minute, time.Hour*24*31), //nolint:mnd
		}
		storageMaxRequestsFlag = cli.UintFlag{
			Name:     "max-requests",
			Usage:    "maximal number of requests to store in the storage (zero means unlimited)",
			Value:    128, //nolint:mnd
			Sources:  cli.EnvVars("MAX_REQUESTS"),
			OnlyOnce: true,
			Validator: func(n uint) error {
				if n > math.MaxUint16 {
					return fmt.Errorf("too big number of requests [%d]", n)
				}

				return nil
			},
		}
		storageFsDirFlag = cli.StringFlag{
			Name:     "fs-storage-dir",
			Usage:    "path to the directory for local fs storage (directory must exist)",
			Sources:  cli.EnvVars("FS_STORAGE_DIR"),
			OnlyOnce: true,
			Validator: func(s string) error {
				if stat, err := os.Stat(s); err == nil && !stat.IsDir() {
					return fmt.Errorf("not a directory [%s]", s)
				}

				return nil
			},
		}
		maxRequestPayloadSizeFlag = cli.UintFlag{
			Name:     "max-request-body-size",
			Usage:    "maximal webhook request body size (in bytes), zero means unlimited",
			Value:    0,
			Sources:  cli.EnvVars("MAX_REQUEST_BODY_SIZE"),
			OnlyOnce: true,
			Validator: func(n uint) error {
				if n > math.MaxUint32 {
					return fmt.Errorf("too big request body size [%d]", n)
				}

				return nil
			},
		}
		autoCreateSessionsFlag = cli.BoolFlag{
			Name:     "auto-create-sessions",
			Usage:    "automatically create sessions for incoming requests",
			Sources:  cli.EnvVars("AUTO_CREATE_SESSIONS"),
			OnlyOnce: true,
		}
		pubSubDriverFlag = cli.StringFlag{
			Name:     "pubsub-driver",
			Value:    pubSubDriverMemory,
			Usage:    "pub/sub driver (" + strings.Join([]string{pubSubDriverMemory, pubSubDriverRedis}, "/") + ")",
			Sources:  cli.EnvVars("PUBSUB_DRIVER"),
			OnlyOnce: true,
			Config:   cli.StringConfig{TrimSpace: true},
			Validator: func(s string) error {
				switch s {
				case pubSubDriverMemory, pubSubDriverRedis:
					return nil
				default:
					return fmt.Errorf("wrong pub/sub driver [%s]", s)
				}
			},
		}
		tunnelDriverFlag = cli.StringFlag{
			Name:     "tunnel-driver",
			Category: tunnelCategory,
			Value:    "", // no driver by default
			Usage: "tunnel driver to expose your locally running app to the internet " +
				"(" + strings.Join([]string{tunnelDriverNgrok}, "/") + ", empty to disable)",
			Sources:  cli.EnvVars("TUNNEL_DRIVER"),
			OnlyOnce: true,
			Config:   cli.StringConfig{TrimSpace: true},
			Validator: func(s string) error {
				switch s {
				case "":
					return nil // no tunnel
				case tunnelDriverNgrok:
					return nil // ngrok
				default:
					return fmt.Errorf("wrong tunnel driver [%s]", s)
				}
			},
		}
		ngrokAuthTokenFlag = cli.StringFlag{
			Name:     "ngrok-auth-token",
			Category: tunnelCategory,
			Usage: "ngrok authentication token (required for ngrok tunnel; create a new one " +
				"at https://dashboard.ngrok.com/authtokens/new)",
			Sources:  cli.EnvVars("NGROK_AUTHTOKEN"),
			OnlyOnce: true,
			Config:   cli.StringConfig{TrimSpace: true},
		}
		publicURLRootFlag = cli.StringFlag{
			Name:     "public-url-root",
			Category: httpCategory,
			Usage: "public URL root override for webhook URLs (e.g., http://webhook-tester.k8s.internal); " +
				"if not set, the URL shown in the UI is based on the browser's location",
			Sources:   cli.EnvVars("PUBLIC_URL_ROOT"),
			OnlyOnce:  true,
			Config:    cli.StringConfig{TrimSpace: true},
			Validator: validateURL,
		}
		redisServerDsnFlag = cli.StringFlag{
			Name: "redis-dsn",
			Usage: "redis-like (redis, keydb) server DSN (e.g. redis://user:pwd@127.0.0.1:6379/0 or " +
				"unix://user:pwd@/path/to/redis.sock?db=0)",
			Value:     "redis://127.0.0.1:6379/0",
			Sources:   cli.EnvVars("REDIS_DSN"),
			OnlyOnce:  true,
			Config:    cli.StringConfig{TrimSpace: true},
			Validator: func(s string) (err error) { _, err = redis.ParseURL(s); return }, //nolint:nlreturn
		}
		shutdownTimeoutFlag = cli.DurationFlag{
			Name:      "shutdown-timeout",
			Usage:     "maximum duration for graceful shutdown",
			Value:     time.Second * 15, //nolint:mnd
			Sources:   cli.EnvVars("SHUTDOWN_TIMEOUT"),
			OnlyOnce:  true,
			Validator: validateDuration("shutdown timeout", time.Millisecond, time.Minute),
		}
		useLiveFrontendFlag = cli.BoolFlag{
			Name:     "use-live-frontend",
			Usage:    "use frontend from the local directory instead of the embedded one (useful for development)",
			OnlyOnce: true,
		}
	)

	cmd.c = &cli.Command{
		Name:    "start",
		Usage:   "Start HTTP/HTTPs servers",
		Aliases: []string{"s", "server", "serve", "http-server"},
		Action: func(ctx context.Context, c *cli.Command) error {
			var opt = &cmd.options

			// set options
			opt.addr = c.String(httpAddrFlag.Name)
			opt.http.tcpPort = uint16(c.Uint(httpPortFlag.Name)) //nolint:gosec
			opt.timeouts.httpRead = c.Duration(httpReadTimeoutFlag.Name)
			opt.timeouts.httpWrite = c.Duration(httpWriteTimeoutFlag.Name)
			opt.timeouts.httpIdle = c.Duration(httpIdleTimeoutFlag.Name)
			opt.storage.driver = c.String(storageDriverFlag.Name)
			opt.storage.sessionTTL = c.Duration(storageSessionTTLFlag.Name)
			opt.storage.maxRequests = uint16(c.Uint(storageMaxRequestsFlag.Name)) //nolint:gosec
			opt.storage.fsDir = c.String(storageFsDirFlag.Name)
			opt.storage.sqlitePath = c.String(sqlitePathFlag.Name)
			opt.auth.token = c.String(authTokenFlag.Name)
			opt.identifiers.keys = c.StringSlice(identifierKeysFlag.Name)
			opt.identifiers.headers = c.StringSlice(identifierHeadersFlag.Name)
			opt.response.scriptTimeout = c.Duration(responseScriptTimeoutFlag.Name)
			opt.hotIndex.window = c.Duration(hotIndexWindowFlag.Name)
			opt.maxRequestPayloadSize = uint32(c.Uint(maxRequestPayloadSizeFlag.Name)) //nolint:gosec
			opt.autoCreateSessions = c.Bool(autoCreateSessionsFlag.Name)
			opt.pubSub.driver = c.String(pubSubDriverFlag.Name)
			opt.tunnel.driver = c.String(tunnelDriverFlag.Name)
			opt.ngrok.authToken = c.String(ngrokAuthTokenFlag.Name)
			opt.redis.dsn = c.String(redisServerDsnFlag.Name)
			opt.timeouts.shutdown = c.Duration(shutdownTimeoutFlag.Name)
			opt.frontend.useLive = c.Bool(useLiveFrontendFlag.Name)
			opt.publicURLRoot = c.String(publicURLRootFlag.Name)

			if opt.tunnel.driver == tunnelDriverNgrok && opt.ngrok.authToken == "" {
				return fmt.Errorf("ngrok authentication token (--%s or %s) is required",
					ngrokAuthTokenFlag.Name, ngrokAuthTokenFlag.Sources.String(),
				)
			}

			return cmd.Run(ctx, log)
		},
		Flags: []cli.Flag{
			&httpAddrFlag,
			&httpPortFlag,
			&httpReadTimeoutFlag,
			&httpWriteTimeoutFlag,
			&httpIdleTimeoutFlag,
			&storageDriverFlag,
			&storageSessionTTLFlag,
			&storageMaxRequestsFlag,
			&storageFsDirFlag,
			&sqlitePathFlag,
			&authTokenFlag,
			&identifierKeysFlag,
			&identifierHeadersFlag,
			&responseScriptTimeoutFlag,
			&hotIndexWindowFlag,
			&maxRequestPayloadSizeFlag,
			&autoCreateSessionsFlag,
			&pubSubDriverFlag,
			&tunnelDriverFlag,
			&ngrokAuthTokenFlag,
			&publicURLRootFlag,
			&redisServerDsnFlag,
			&shutdownTimeoutFlag,
			&useLiveFrontendFlag,
		},
		Commands: []*cli.Command{
			healthcheck.NewCommand(defaultHttpPort),
		},
	}

	return cmd.c
}

// validateDuration returns a validator for the given duration.
func validateDuration(name string, minValue, maxValue time.Duration) func(d time.Duration) error {
	return func(d time.Duration) error {
		switch {
		case d < 0:
			return fmt.Errorf("negative %s (%s)", name, d)
		case d < minValue:
			return fmt.Errorf("too small %s (%s)", name, d)
		case d > maxValue:
			return fmt.Errorf("too big %s (%s)", name, d)
		}

		return nil
	}
}

// validateURL validates a URL flag value, ensuring it has a scheme and host.
func validateURL(s string) error {
	if s == "" {
		return nil // empty is valid (means no override)
	}

	u, err := url.Parse(s)
	if err != nil {
		return fmt.Errorf("invalid URL [%s]: %w", s, err)
	}

	if u.Scheme == "" || u.Host == "" {
		return fmt.Errorf("URL must include scheme and host [%s]", s)
	}

	return nil
}

// Run current command.
func (cmd *command) Run(parentCtx context.Context, log *zap.Logger) error { //nolint:funlen,gocyclo,gocognit
	ctx, cancel := context.WithCancel(parentCtx)
	defer cancel()

	var rdc *redis.Client // may be nil

	// establish connection to Redis server if needed
	if cmd.options.pubSub.driver == pubSubDriverRedis || cmd.options.storage.driver == storageDriverRedis {
		var opt, pErr = redis.ParseURL(cmd.options.redis.dsn)
		if pErr != nil {
			return fmt.Errorf("failed to parse Redis DSN: %w", pErr)
		}

		{ // disable maintenance notifications (https://github.com/tarampampam/webhook-tester/issues/713)
			if opt.MaintNotificationsConfig == nil {
				opt.MaintNotificationsConfig = new(maintnotifications.Config)
			}

			opt.MaintNotificationsConfig.Mode = maintnotifications.ModeDisabled
		}

		rdc = redis.NewClient(opt)

		redis.SetLogger(logger.NewRedisBridge(log.Named("redis")))

		defer func() { _ = rdc.Close() }()

		if err := rdc.Ping(ctx).Err(); err != nil {
			return fmt.Errorf("failed to ping Redis server: %w", err)
		}
	}

	// Build the identifier extractor ONCE and share the SAME instance between the
	// durable index (SQLite, via WithSQLiteExtractor) and the webhook middleware /
	// in-memory hot index, so durable and in-memory paths extract identifiers
	// identically. Query-param scanning is always enabled.
	var extractor = identifiers.NewExtractor(
		cmd.options.identifiers.keys,
		cmd.options.identifiers.headers,
		true,
	)

	var db storage.Storage

	// create the storage
	switch cmd.options.storage.driver {
	case storageDriverSQLite:
		var sq, sErr = storage.NewSQLite(
			ctx,
			"file:"+cmd.options.storage.sqlitePath,
			cmd.options.storage.sessionTTL,
			uint32(cmd.options.storage.maxRequests),
			storage.WithSQLiteExtractor(extractor.Extract),
		)
		if sErr != nil {
			return fmt.Errorf("failed to open sqlite storage [%s]: %w", cmd.options.storage.sqlitePath, sErr)
		}

		defer func() { _ = sq.Close() }()

		db = sq
	case storageDriverMemory:
		var inMemory = storage.NewInMemory(cmd.options.storage.sessionTTL, uint32(cmd.options.storage.maxRequests)) //nolint:contextcheck,lll
		defer func() { _ = inMemory.Close() }()
		db = inMemory //nolint:wsl_v5
	case storageDriverRedis:
		db = storage.NewRedis(rdc, cmd.options.storage.sessionTTL, uint32(cmd.options.storage.maxRequests))
	case storageDriverFS:
		if stat, err := os.Stat(cmd.options.storage.fsDir); err != nil {
			return fmt.Errorf("failed to get the storage directory [%s]: %w", cmd.options.storage.fsDir, err)
		} else if !stat.IsDir() {
			return fmt.Errorf("not a directory [%s]", cmd.options.storage.fsDir)
		}

		var fs = storage.NewFS( //nolint:contextcheck
			cmd.options.storage.fsDir,
			cmd.options.storage.sessionTTL,
			uint32(cmd.options.storage.maxRequests),
		)

		defer func() { _ = fs.Close() }()

		db = fs
	default:
		return fmt.Errorf("unknown storage driver [%s]", cmd.options.storage.driver)
	}

	var pubSub pubsub.PubSub[pubsub.RequestEvent]

	// create the Pub/Sub
	switch cmd.options.pubSub.driver {
	case pubSubDriverMemory:
		pubSub = pubsub.NewInMemory[pubsub.RequestEvent]()
	case pubSubDriverRedis:
		pubSub = pubsub.NewRedis[pubsub.RequestEvent](rdc, encoding.JSON{})
	default:
		return fmt.Errorf("unknown Pub/Sub driver [%s]", cmd.options.pubSub.driver)
	}

	var httpLog = log.Named("http")

	var appSettings = config.AppSettings{
		MaxRequests:        cmd.options.storage.maxRequests,
		MaxRequestBodySize: cmd.options.maxRequestPayloadSize,
		SessionTTL:         cmd.options.storage.sessionTTL,
		AutoCreateSessions: cmd.options.autoCreateSessions,
		AuthToken:          cmd.options.auth.token,
	}

	// parse public URL root if provided
	if cmd.options.publicURLRoot != "" {
		parsedURL, err := url.Parse(cmd.options.publicURLRoot)
		if err != nil {
			return fmt.Errorf("parse public URL root %q: %w", cmd.options.publicURLRoot, err)
		}

		appSettings.PublicURLRoot = parsedURL
	}

	// in-memory hot index (search fast path), warm-started from durable storage so it
	// is correct after a restart, and kept bounded by a background eviction janitor.
	var hi = hotindex.New(cmd.options.hotIndex.window)

	warmHotIndex(ctx, db, hi, cmd.options.hotIndex.window, log.Named("hotindex"))

	go runHotIndexJanitor(ctx, hi, cmd.options.hotIndex.window)

	// create HTTP server
	var server = appHttp.NewServer(ctx, httpLog,
		appHttp.WithReadTimeout(cmd.options.timeouts.httpRead),
		appHttp.WithWriteTimeout(cmd.options.timeouts.httpWrite),
		appHttp.WithIDLETimeout(cmd.options.timeouts.httpIdle),
	).Register(
		ctx,
		httpLog,
		cmd.readinessChecker(rdc),
		cmd.latestAppVersionGetter(),
		&appSettings,
		db,
		pubSub,
		extractor,
		hi,
		cmd.options.response.scriptTimeout,
		cmd.options.frontend.useLive,
	)

	server.ShutdownTimeout = cmd.options.timeouts.shutdown // set shutdown timeout

	// open HTTP port
	httpLn, httpLnErr := net.Listen("tcp", fmt.Sprintf("%s:%d", cmd.options.addr, cmd.options.http.tcpPort))
	if httpLnErr != nil {
		return fmt.Errorf("HTTP port error (%s:%d): %w", cmd.options.addr, cmd.options.http.tcpPort, httpLnErr)
	}

	// start HTTP server in separate goroutine
	go func() {
		defer func() { _ = httpLn.Close() }()

		log.Info("HTTP server starting",
			zap.String("address", cmd.options.addr),
			zap.Uint16("port", cmd.options.http.tcpPort),
			zap.String("storage", cmd.options.storage.driver),
			zap.String("pubsub", cmd.options.pubSub.driver),
			zap.String("open", fmt.Sprintf("http://%s:%d", func() string {
				if addr := cmd.options.addr; addr == "0.0.0.0" || addr == "::" || strings.HasPrefix(addr, "127.") {
					return "127.0.0.1"
				}

				return cmd.options.addr
			}(), cmd.options.http.tcpPort)),
		)

		var tun tunnel.Tunneler

		if cmd.options.tunnel.driver == tunnelDriverNgrok {
			tun = tunnel.NewNgrok(cmd.options.ngrok.authToken, tunnel.WithNgrokLogger(log.Named("ngrok")))
		}

		if tun != nil {
			defer func() { _ = tun.Close() }()

			if pubUrl, err := tun.Expose(ctx, cmd.options.http.tcpPort); err != nil {
				log.Error("Failed to start tunnel", zap.Error(err))
			} else {
				log.Info("Tunnel started", zap.String("url", pubUrl))

				if u, uErr := url.Parse(pubUrl); uErr == nil {
					// FIXME: concurrent write to the appSettings without mutex
					appSettings.TunnelEnabled, appSettings.TunnelURL = true, u
				}
			}
		}

		if err := server.StartHTTP(ctx, httpLn); err != nil {
			cancel() // cancel the context on error (this is critical for us)

			log.Error("Failed to start HTTP server", zap.Error(err))
		} else {
			log.Debug("HTTP server stopped")
		}
	}()

	// here, we are blocking until the context is canceled. this will occur when the user sends a signal to stop
	// the app by pressing Ctrl+C, terminating the process, or if the HTTP/HTTPS server fails to start
	<-ctx.Done()

	// if the context contains an error, and it's not a cancellation error, return it
	if err := ctx.Err(); err != nil && !errors.Is(err, context.Canceled) {
		return err
	}

	return nil
}

// readinessChecker returns a readiness checker. Feel free to add more checks/dependencies here if needed.
func (cmd *command) readinessChecker(rdc *redis.Client) func(ctx context.Context) error {
	return func(ctx context.Context) error {
		if rdc == nil {
			return nil
		}

		return rdc.Ping(ctx).Err()
	}
}

// latestAppVersionGetter returns a function to get the latest app version.
func (cmd *command) latestAppVersionGetter() func(ctx context.Context) (string, error) {
	return func(ctx context.Context) (string, error) { return version.Latest(ctx) }
}

// recentIdentifierLister is the optional storage capability used to warm-start the
// in-memory hot index from durable storage. Only the SQLite driver implements it;
// drivers that do not are left to the (correct, slower) durable scan search path.
type recentIdentifierLister interface {
	ListRecentIdentifiers(context.Context, int64) ([]storage.IdentifierRef, error)
}

// buildHotIndexMap converts durable identifier rows into the composite-keyed map
// expected by hotindex.Rebuild. The map key MUST be lower(key)+"\x00"+value to
// mirror the hot index / SQLite normalization contract.
func buildHotIndexMap(refs []storage.IdentifierRef) map[string][]hotindex.Ref {
	var out = make(map[string][]hotindex.Ref, len(refs))

	for _, r := range refs {
		var k = strings.ToLower(r.Key) + "\x00" + r.Value

		out[k] = append(out[k], hotindex.Ref{
			SessionID:           r.SessionID,
			SessionSlug:         r.SessionSlug,
			RequestID:           r.RequestID,
			CapturedAtUnixMilli: r.CapturedAtUnixMilli,
		})
	}

	return out
}

// warmHotIndex back-fills the hot index from durable storage so identifier search is
// correct immediately after a restart. When the driver lacks the capability the index
// is intentionally left un-warmed: readers then fall back to the durable scan path.
func warmHotIndex(
	ctx context.Context,
	db storage.Storage,
	hi *hotindex.HotIndex,
	window time.Duration,
	log *zap.Logger,
) {
	lister, ok := db.(recentIdentifierLister)
	if !ok {
		log.Debug("storage driver has no recent-identifier capability; hot index left cold")

		return
	}

	var cutoff = time.Now().Add(-window).UnixMilli()

	refs, err := lister.ListRecentIdentifiers(ctx, cutoff)
	if err != nil {
		log.Warn("hot index warm-up failed; search falls back to the durable scan path", zap.Error(err))

		return
	}

	hi.Rebuild(buildHotIndexMap(refs)) // also marks the index warmed

	log.Debug("hot index warmed from durable storage", zap.Int("identifiers", len(refs)))
}

// runHotIndexJanitor periodically evicts hot-index entries older than the retention
// window. It runs until ctx is canceled (on shutdown), so it does not leak.
func runHotIndexJanitor(ctx context.Context, hi *hotindex.HotIndex, window time.Duration) {
	var interval = window / 24 // sweep ~24x per window
	if interval > time.Hour {
		interval = time.Hour
	}

	if interval < time.Minute {
		interval = time.Minute
	}

	var t = time.NewTicker(interval)
	defer t.Stop()

	for {
		select {
		case <-ctx.Done():
			return
		case <-t.C:
			hi.Evict(time.Now())
		}
	}
}
