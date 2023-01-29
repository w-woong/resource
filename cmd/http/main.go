package main

import (
	"crypto/tls"
	"flag"
	"fmt"
	"os"
	"runtime"
	"strconv"
	"strings"
	"syscall"
	"time"

	"github.com/go-wonk/si"
	"github.com/go-wonk/si/sigorm"
	"github.com/go-wonk/si/sihttp"
	"github.com/gorilla/mux"
	"github.com/w-woong/common"
	commonadapter "github.com/w-woong/common/adapter"
	"github.com/w-woong/common/configs"
	"github.com/w-woong/common/logger"
	commonport "github.com/w-woong/common/port"
	"github.com/w-woong/common/txcom"
	"github.com/w-woong/common/utils"
	"github.com/w-woong/common/wrapper"
	"github.com/w-woong/resource/cmd/route"
	"gorm.io/gorm"

	// "go.elastic.co/apm/module/apmgorilla/v2"
	postgresapm "go.elastic.co/apm/module/apmgormv2/v2/driver/postgres" // postgres with gorm
	// _ "go.elastic.co/apm/module/apmsql/v2/pq" // postgres sql with pq
	"go.elastic.co/apm/v2"
)

var (
	Version = "undefined"

	printVersion     bool
	tickIntervalSec  int = 30
	addr             string
	certPem, certKey string
	readTimeout      int
	writeTimeout     int
	configName       string
	maxProc          int

	usePprof    = false
	pprofAddr   = ":56060"
	autoMigrate = false
)

func init() {
	flag.StringVar(&addr, "addr", ":49006", "listen address")
	flag.BoolVar(&printVersion, "version", false, "print version")
	flag.IntVar(&tickIntervalSec, "tick", 30, "tick interval in second")
	flag.StringVar(&certKey, "key", "./certs/key.pem", "server key")
	flag.StringVar(&certPem, "pem", "./certs/cert.pem", "server pem")
	flag.IntVar(&readTimeout, "readTimeout", 30, "read timeout")
	flag.IntVar(&writeTimeout, "writeTimeout", 30, "write timeout")
	flag.StringVar(&configName, "config", "./configs/server.yml", "config file name")
	flag.IntVar(&maxProc, "mp", runtime.NumCPU(), "GOMAXPROCS")

	flag.BoolVar(&usePprof, "pprof", false, "use pprof")
	flag.StringVar(&pprofAddr, "pprof_addr", ":56060", "pprof listen address")
	flag.BoolVar(&autoMigrate, "autoMigrate", false, "auto migrate")

	flag.Parse()
}

func main() {
	// defaultTimeout := 6 * time.Second

	var err error

	if printVersion {
		fmt.Printf("version \"%v\"\n", Version)
		return
	}
	runtime.GOMAXPROCS(maxProc)

	// apm
	apmActive, _ := strconv.ParseBool(os.Getenv("ELASTIC_APM_ACTIVE"))
	if apmActive {
		tracer := apm.DefaultTracer()
		defer tracer.Flush(nil)
	}

	// config
	conf := common.Config{}
	if err := configs.ReadConfigInto(configName, &conf); err != nil {
		fmt.Println(err)
		os.Exit(1)
	}

	// logger
	logger.Open(conf.Logger.Level, conf.Logger.Stdout,
		conf.Logger.File.Name, conf.Logger.File.MaxSize, conf.Logger.File.MaxBackup,
		conf.Logger.File.MaxAge, conf.Logger.File.Compressed)
	defer logger.Close()

	// gorm
	var gormDB *gorm.DB
	switch conf.Server.Repo.Driver {
	case "pgx":
		if apmActive {
			gormDB, err = gorm.Open(postgresapm.Open(conf.Server.Repo.ConnStr),
				&gorm.Config{Logger: logger.OpenGormLogger(conf.Server.Repo.LogLevel)},
			)
			if err != nil {
				logger.Error(err.Error())
				os.Exit(1)
			}
			db, err := gormDB.DB()
			if err != nil {
				logger.Error(err.Error())
				os.Exit(1)
			}
			defer db.Close()
		} else {
			// db
			// var db *sql.DB
			db, err := si.OpenSqlDB(conf.Server.Repo.Driver, conf.Server.Repo.ConnStr,
				conf.Server.Repo.MaxIdleConns, conf.Server.Repo.MaxOpenConns, time.Duration(conf.Server.Repo.ConnMaxLifetimeMinutes)*time.Minute)
			if err != nil {
				logger.Error(err.Error())
				os.Exit(1)
			}
			defer db.Close()

			gormDB, err = sigorm.OpenPostgresWithConfig(db,
				&gorm.Config{Logger: logger.OpenGormLogger(conf.Server.Repo.LogLevel)},
			)
			if err != nil {
				logger.Error(err.Error())
				os.Exit(1)
			}
		}

	default:
		logger.Error(conf.Server.Repo.Driver + " is not allowed")
		os.Exit(1)
	}

	var txBeginner common.TxBeginner
	var isolationLvl common.IsolationLevelSetter
	switch conf.Server.Repo.Driver {
	case "pgx":
		txBeginner = txcom.NewGormTxBeginner(gormDB)
		isolationLvl = txcom.NewGormIsolationLevelSetter()
		fmt.Println(txBeginner, isolationLvl) // TODO
	default:
		logger.Error(conf.Server.Repo.Driver + " is not allowed")
		os.Exit(1)
	}

	// if autoMigrate {
	// 	gormDB.AutoMigrate(&entity.Cart{}, &entity.CartProduct{})
	// }

	// usc := usecase.NewCartUsc(txBeginner, isolationLvl, cartRepo, cartProductRepo)

	// oauth2
	idTokenValidators := make(commonport.IDTokenValidators)
	for _, v := range conf.Client.Oauth2.IDTokenValidators {
		if v.Type == "jwks" {
			jwksUrl, err := utils.GetJwksUrl(v.OpenIDConfUrl)
			if err != nil {
				logger.Error(err.Error())
				os.Exit(1)
			}
			jwksStore, err := utils.NewJwksCache(jwksUrl)
			if err != nil {
				logger.Error(err.Error())
				os.Exit(1)
			}
			validator := commonadapter.NewJwksIDTokenValidator(jwksStore, v.Token.TokenSourceKeyName, v.Token.IDKeyName, v.Token.IDTokenKeyName)
			idTokenValidators[v.Token.Source] = validator
		}
	}

	var userSvc commonport.UserSvc
	if conf.Client.UserHttp.Url != "" {
		userSvc = commonadapter.NewUserHttp(sihttp.DefaultInsecureClient(),
			// conf.Client.Oauth2.Token.Source,
			conf.Client.UserHttp.Url,
			conf.Client.UserHttp.BearerToken,
			conf.Client.Oauth2.Token.TokenSourceKeyName,
			conf.Client.Oauth2.Token.IDKeyName, conf.Client.Oauth2.Token.IDTokenKeyName)
	} else if conf.Client.UserGrpc.Addr != "" {
		conn, err := wrapper.NewGrpcClient(conf.Client.UserGrpc, false)
		if err != nil {
			logger.Error(err.Error())
			os.Exit(1)
		}
		userSvc = commonadapter.NewUserGrpc(conn)
	} else {
		userSvc = commonadapter.NewUserSvcNop()
	}

	// http route
	router := mux.NewRouter()
	route.ResourceRoute(router, conf.Server.Http, idTokenValidators, userSvc)

	// http server
	tlsConfig := sihttp.CreateTLSConfigMinTls(tls.VersionTLS12)
	httpServer := sihttp.NewServerCors(router, tlsConfig, addr,
		time.Duration(writeTimeout)*time.Second, time.Duration(readTimeout)*time.Second,
		certPem, certKey,
		strings.Split(conf.Server.Http.AllowedOrigins, ","),
		strings.Split(conf.Server.Http.AllowedHeaders, ","),
		strings.Split(conf.Server.Http.AllowedMethods, ","),
	)

	// ticker
	ticker := time.NewTicker(time.Duration(tickIntervalSec) * time.Second)
	tickerDone := make(chan bool)
	common.StartTicker(tickerDone, ticker, func(t time.Time) {
		logger.Info(fmt.Sprintf("NoOfGR:%v, %v", runtime.NumGoroutine(), t))
	})

	// signal, wait for it to shutdown http server.
	common.StartSignalStopper(httpServer, syscall.SIGINT, syscall.SIGTERM)

	// start
	logger.Info("start listening on " + addr)
	if err = httpServer.Start(); err != nil {
		logger.Error(err.Error())
	}

	// finish
	ticker.Stop()
	tickerDone <- true
	logger.Info("finished")
}
