package route

import (
	"net/http"
	"time"

	"github.com/gorilla/mux"
	"github.com/w-woong/common"
	commonport "github.com/w-woong/common/port"

	"github.com/w-woong/resource/delivery"
)

func ResourceRoute(router *mux.Router, conf common.ConfigHttp,
	validator commonport.IDTokenValidators, userSvc commonport.UserSvc) *delivery.ResourceHttpHandler {

	handler := delivery.NewResourceHttpHandler(time.Duration(conf.Timeout) * time.Second)

	fileServer := http.FileServer(http.Dir("./resource/"))
	router.PathPrefix("/v1/resource/").Handler(http.StripPrefix("/v1/resource/", handleFileServe(fileServer)))

	// router.HandleFunc("/v1/order/cart", middlewares.AuthIDTokenUserAccountHandler(
	// 	handler.HandleFindByUserID, validator, userSvc,
	// )).Methods(http.MethodGet)
	// router.HandleFunc("/v1/order/cart/_find-or-create", middlewares.AuthIDTokenUserAccountHandler(
	// 	handler.HandleFindOrCreateByUserID, validator, userSvc,
	// )).Methods(http.MethodGet)

	// router.HandleFunc("/test/order/cart", middlewares.AuthIDTokenUserAccountHandler(
	// 	handler.HandleTestRefreshError, validator, userSvc,
	// )).Methods(http.MethodGet)

	// router.HandleFunc("/v1/order/cart/product", middlewares.AuthIDTokenUserAccountHandler(
	// 	handler.HandleAddCartProduct, validator, userSvc,
	// )).Methods(http.MethodPost)

	return handler
}

func handleFileServe(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		next.ServeHTTP(w, r)
	})
}
