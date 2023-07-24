package reverseproxy

import (
	"context"
	"fmt"
	"math/big"
	"net/http"
	"net/http/httputil"
	"net/url"

	"github.com/ethereum/go-ethereum/common"
	"github.com/ethereum/go-ethereum/common/hexutil"
	"github.com/rs/zerolog"
	"github.com/statechannels/go-nitro/crypto"
	"github.com/statechannels/go-nitro/payments"
	"github.com/statechannels/go-nitro/rpc"
	"github.com/statechannels/go-nitro/types"
)

const (
	AMOUNT_VOUCHER_PARAM     = "amount"
	CHANNEL_ID_VOUCHER_PARAM = "channelId"
	SIGNATURE_VOUCHER_PARAM  = "signature"
)

// ReversePaymentProxy is an HTTP proxy that charges for HTTP requests.
type ReversePaymentProxy struct {
	server                *http.Server
	nitroClient           *rpc.RpcClient
	expectedPaymentAmount *big.Int
	reverseProxy          *httputil.ReverseProxy
	logger                zerolog.Logger
}

// NewReversePaymentProxy creates a new ReversePaymentProxy.
func NewReversePaymentProxy(proxyAddress string, nitroEndpoint string, destinationURL string, expectedPaymentAmount *big.Int, logger zerolog.Logger) *ReversePaymentProxy {
	server := &http.Server{Addr: proxyAddress}

	nitroClient, err := rpc.NewHttpRpcClient(nitroEndpoint)
	if err != nil {
		panic(err)
	}
	destinationUrl, err := url.Parse(destinationURL)
	if err != nil {
		panic(err)
	}
	// Creates a reverse proxy that will handle forwarding requests to the destination server

	proxy := &httputil.ReverseProxy{
		Rewrite: func(r *httputil.ProxyRequest) {
			// SetURL updates the URL and will update the host header with the proxy
			// This avoids problems with servers that check the host header against the requestor
			r.SetURL(destinationUrl)
		},
	}

	return &ReversePaymentProxy{
		server:                server,
		logger:                logger,
		nitroClient:           nitroClient,
		reverseProxy:          proxy,
		expectedPaymentAmount: expectedPaymentAmount,
	}
}

// Start starts the proxy server in a goroutine.
func (p *ReversePaymentProxy) Start() error {
	// Wire up our proxy to the http handler
	// This means that p.ServeHTTP will be called for every request
	p.server.Handler = p

	go func() {
		p.logger.Info().Msgf("Starting reverse payment proxy listening on %s.", p.server.Addr)
		p.logger.Info().Msgf("Each request will cost %d wei", p.expectedPaymentAmount.Uint64())
		if err := p.server.ListenAndServe(); err != http.ErrServerClosed {
			p.logger.Err(err).Msg("ListenAndServe()")
		}
	}()

	return nil
}

// Stop stops the proxy server and closes everything.
func (p *ReversePaymentProxy) Stop() error {
	p.logger.Info().Msgf("Stopping reverse payment proxy listening on %s", p.server.Addr)
	err := p.server.Shutdown(context.Background())
	if err != nil {
		return err
	}

	return p.nitroClient.Close()
}

// ServeHTTP is the main entry point for the proxy.
// It looks for voucher parameters in the request to construct a voucher.
// It then passes the voucher to the nitro client to process.
// Based on the amount added by the voucher, it either forwards the request to the destination server or returns an error.
func (p *ReversePaymentProxy) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	p.logger.Debug().Msgf("Incoming request URL %s", r.URL.String())
	params, err := url.ParseQuery(r.URL.RawQuery)
	if err != nil {
		p.webError(w, fmt.Errorf("could not parse query params: %w", err), http.StatusBadRequest)
		return
	}

	v, err := parseVoucher(params)
	if err != nil {
		p.webError(w, fmt.Errorf("could not parse voucher: %w", err), http.StatusPaymentRequired)
		return
	}

	s, err := p.nitroClient.ReceiveVoucher(v)
	if err != nil {
		p.webError(w, fmt.Errorf("error processing voucher %w", err), http.StatusPaymentRequired)
		return
	}

	p.logger.Debug().Msgf("Received voucher with delta %d", s.Delta.Uint64())
	// s.Delta is amount our balance increases by adding this voucher
	// AKA the payment amount we received in the request for this file
	if s.Delta.Cmp(p.expectedPaymentAmount) < 0 {
		p.webError(w, fmt.Errorf("payment of %d required, the voucher only resulted in a payment of %d", p.expectedPaymentAmount.Uint64(), s.Delta.Uint64()), http.StatusPaymentRequired)
		return
	}

	// Strip out the voucher params so the destination server doesn't need to handle them
	removeVoucherParams(r.URL)

	// Forward the request to the destination server
	p.reverseProxy.ServeHTTP(w, r)
	p.logger.Debug().Msgf("Destination request URL %s", r.URL.String())
}

// webError is a helper function to return an http error.
func (p *ReversePaymentProxy) webError(w http.ResponseWriter, err error, code int) {
	// TODO: This is a hack to allow CORS requests to the gateway for the boost integration demo.
	w.Header().Set("Access-Control-Allow-Origin", "*")
	w.Header().Set("Access-Control-Allow-Headers", "*")

	http.Error(w, err.Error(), code)
	p.logger.Error().Err(err).Msgf("Error processing request")
}

// parseVoucher takes in an a collection of query params and parses out a voucher.
func parseVoucher(params url.Values) (payments.Voucher, error) {
	if !params.Has(CHANNEL_ID_VOUCHER_PARAM) {
		return payments.Voucher{}, fmt.Errorf("a valid channel id must be provided")
	}
	if !params.Has(AMOUNT_VOUCHER_PARAM) {
		return payments.Voucher{}, fmt.Errorf("a valid amount must be provided")
	}
	if !params.Has(SIGNATURE_VOUCHER_PARAM) {
		return payments.Voucher{}, fmt.Errorf("a valid signature must be provided")
	}
	rawChId := params.Get(CHANNEL_ID_VOUCHER_PARAM)
	rawAmt := params.Get(AMOUNT_VOUCHER_PARAM)
	amount := big.NewInt(0)
	amount.SetString(rawAmt, 10)
	rawSignature := params.Get(SIGNATURE_VOUCHER_PARAM)

	v := payments.Voucher{
		ChannelId: types.Destination(common.HexToHash(rawChId)),
		Amount:    amount,
		Signature: crypto.SplitSignature(hexutil.MustDecode(rawSignature)),
	}
	return v, nil
}

// removeVoucherParams removes the voucher parameters from the request URL.
func removeVoucherParams(u *url.URL) {
	queryParams := u.Query()
	delete(queryParams, CHANNEL_ID_VOUCHER_PARAM)
	delete(queryParams, SIGNATURE_VOUCHER_PARAM)
	delete(queryParams, AMOUNT_VOUCHER_PARAM)
	// Update the request URL without the voucher parameters
	u.RawQuery = queryParams.Encode()
}
