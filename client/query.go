package client

import (
	"fmt"
	"math/big"

	"github.com/statechannels/go-nitro/channel"
	"github.com/statechannels/go-nitro/channel/state"
	"github.com/statechannels/go-nitro/channel/state/outcome"
	"github.com/statechannels/go-nitro/client/engine/store"
	"github.com/statechannels/go-nitro/payments"
	"github.com/statechannels/go-nitro/protocols"
	"github.com/statechannels/go-nitro/protocols/virtualdefund"
	"github.com/statechannels/go-nitro/types"
)

type ChannelStatus string

// TODO: Think through statuses
const Proposed ChannelStatus = "Proposed"
const Ready ChannelStatus = "Ready"
const Closing ChannelStatus = "Closing"
const Complete ChannelStatus = "Complete"

// PaymentChannelBalance contains the balance of a uni-directional payment channel
type PaymentChannelBalance struct {
	AssetAddress   types.Address
	Payee          types.Address
	Payer          types.Address
	PaidSoFar      *big.Int
	RemainingFunds *big.Int
}

// PaymentChannelInfo contains balance and status info about a payment channel
type PaymentChannelInfo struct {
	ID      types.Destination
	Status  ChannelStatus
	Balance PaymentChannelBalance
}

// getStatusFromChannel returns the status of the channel
func getStatusFromChannel(c *channel.Channel) ChannelStatus {
	if c.FinalSignedByMe() {
		if c.FinalCompleted() {
			return Complete
		}
		return Closing
	}
	if !c.PostFundComplete() {
		return Proposed
	}

	return Ready
}

func getPaymentChannelBalance(participants []types.Address, outcome outcome.Exit) PaymentChannelBalance {

	numParticipants := len(participants)
	// TODO: We assume single asset outcomes
	sao := outcome[0]
	asset := sao.Asset
	payer := participants[0]
	payee := participants[numParticipants-1]
	paidSoFar := sao.Allocations[1].Amount
	remaining := sao.Allocations[0].Amount
	return PaymentChannelBalance{
		AssetAddress:   asset,
		Payer:          payer,
		Payee:          payee,
		PaidSoFar:      paidSoFar,
		RemainingFunds: remaining,
	}
}

// LedgerChannelInfo contains balance and status info about a ledger channel
type LedgerChannelInfo struct {
	ID      types.Destination
	Status  ChannelStatus
	Balance LedgerChannelBalance
}

// LedgerChannelBalance contains the balance of a ledger channel
type LedgerChannelBalance struct {
	AssetAddress  types.Address
	Hub           types.Address
	Client        types.Address
	HubBalance    *big.Int
	ClientBalance *big.Int
}

// getLatestSupported returns the latest supported state of the channel or the prefund state if no supported state exists
func getLatestSupported(channel *channel.Channel) state.State {
	if channel.HasSupportedState() {
		supported, _ := channel.LatestSupportedState()
		return supported
	}
	return channel.PreFundState()
}

// getLedgerBalanceFromState returns the balance of the ledger channel from the given state
func getLedgerBalanceFromState(latest state.State) LedgerChannelBalance {

	// TODO: We assume single asset outcomes
	outcome := latest.Outcome[0]
	asset := outcome.Asset
	client := latest.Participants[0]
	clientBalance := outcome.Allocations[0].Amount
	hub := latest.Participants[1]
	hubBalance := outcome.Allocations[1].Amount

	return LedgerChannelBalance{
		AssetAddress:  asset,
		Hub:           hub,
		Client:        client,
		HubBalance:    hubBalance,
		ClientBalance: clientBalance,
	}
}

func getPaymentChannelInfo(id types.Destination, store store.Store, vm *payments.VoucherManager) (PaymentChannelInfo, error) {

	// This is slightly awkward but if the virtual defunding objective is complete it won't come back if we query by channel id
	// We manually construct the objective id and query by that
	virtualDefundId := protocols.ObjectiveId(virtualdefund.ObjectivePrefix + id.String())
	fetchedDefund, err := store.GetObjectiveById(virtualDefundId)
	isVirtualDefund := err == nil

	// Since virtual defunding stores all state updates on the objective instead of the store we need to manually check the objective
	if isVirtualDefund {
		defund := fetchedDefund.(*virtualdefund.Objective)
		status := Closing

		if defund.Status == protocols.Completed {
			status = Complete
		}
		return PaymentChannelInfo{
			ID:      id,
			Status:  status,
			Balance: getPaymentChannelBalance(defund.VFixed.Participants, []outcome.SingleAssetExit{defund.FinalOutcome}),
		}, nil
	}

	// Otherwise we can just check the store
	c, ok := store.GetChannelById(id)

	if ok {
		status := getStatusFromChannel(c)
		balance := getPaymentChannelBalance(c.Participants, getLatestSupported(c).Outcome)

		// If we have received vouchers we want to update the channel balance to reflect the vouchers
		if hasVouchers := vm.ChannelRegistered(id); status == Ready && hasVouchers {
			voucherBal, err := vm.Balance(id)
			if err != nil {
				return PaymentChannelInfo{}, err
			}
			balance.PaidSoFar.Set(voucherBal.Paid)
			balance.RemainingFunds.Set(voucherBal.Remaining)
		}

		return PaymentChannelInfo{
			ID:      id,
			Status:  status,
			Balance: balance,
		}, nil
	}
	return PaymentChannelInfo{}, fmt.Errorf("could not find channel with id %v", id)

}

// getLedgerChannelInfo returns the LedgerChannelInfo for the given channel
// It does this by querying the provided store
func getLedgerChannelInfo(id types.Destination, store store.Store) (LedgerChannelInfo, error) {
	c, ok := store.GetChannelById(id)
	if ok {

		return LedgerChannelInfo{
			ID:      c.Id,
			Status:  getStatusFromChannel(c),
			Balance: getLedgerBalanceFromState(getLatestSupported(c)),
		}, nil
	}

	con, err := store.GetConsensusChannelById(id)
	if err != nil {
		return LedgerChannelInfo{}, err
	}

	latest := con.ConsensusVars().AsState(con.FixedPart())
	return LedgerChannelInfo{
		ID:      con.Id,
		Status:  Ready,
		Balance: getLedgerBalanceFromState(latest),
	}, nil

}
