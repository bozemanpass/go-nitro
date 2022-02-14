package ledger

import (
	"fmt"
	"math/big"

	"github.com/statechannels/go-nitro/channel"
	"github.com/statechannels/go-nitro/channel/state"
	"github.com/statechannels/go-nitro/channel/state/outcome"
	"github.com/statechannels/go-nitro/protocols"
	"github.com/statechannels/go-nitro/types"
)

type LedgerCranker struct {
	ledgers map[types.Destination]*channel.TwoPartyLedger
	nonce   *big.Int
}

func NewLedgerCranker() LedgerCranker {
	return LedgerCranker{
		ledgers: make(map[types.Destination]*channel.TwoPartyLedger),
		nonce:   big.NewInt(0),
	}
}

// Update updates the ledger cranker with the given ledger channel
// Eventually this will be deprecated in favour of using store
func (l *LedgerCranker) Update(ledger *channel.TwoPartyLedger) {
	l.ledgers[ledger.Id] = ledger
}

func (l *LedgerCranker) CreateLedger(left outcome.Allocation, right outcome.Allocation, secretKey *[]byte, myIndex uint) *channel.TwoPartyLedger {

	leftAddress, _ := left.Destination.ToAddress()
	rightAddress, _ := right.Destination.ToAddress()
	initialState := state.State{
		ChainId:           big.NewInt(9001),
		Participants:      []types.Address{leftAddress, rightAddress},
		ChannelNonce:      l.nonce,
		AppDefinition:     types.Address{},
		ChallengeDuration: big.NewInt(45),
		AppData:           []byte{},
		Outcome: outcome.Exit{outcome.SingleAssetExit{
			Allocations: outcome.Allocations{left, right},
		}},
		TurnNum: 0,
		IsFinal: false,
	}

	ledger, lErr := channel.NewTwoPartyLedger(initialState, myIndex)
	if lErr != nil {
		panic(lErr)
	}

	l.ledgers[ledger.Id] = ledger
	return ledger
}

// HandleRequest accepts a ledger request and updates the ledger channel based on the request.
func (l *LedgerCranker) HandleRequest(request protocols.LedgerRequest, oId protocols.ObjectiveId, secretKey *[]byte) protocols.SideEffects {

	ledger := l.ledgers[request.LedgerId]

	guarantee, _ := outcome.GuaranteeMetadata{
		Left:  request.Left,
		Right: request.Right,
	}.Encode()

	supported, err := ledger.Channel.LatestSupportedState()
	if err != nil {
		panic(err)
	}

	nextState := supported.Clone()
	// TODO: We're currently setting the amount to 0 for participants, we should calculate the correct amount
	nextState.Outcome = outcome.Exit{outcome.SingleAssetExit{
		Allocations: outcome.Allocations{
			outcome.Allocation{
				Destination: request.Left,
				Amount:      big.NewInt(0),
			},
			outcome.Allocation{
				Destination: request.Right,
				Amount:      big.NewInt(0),
			},
			outcome.Allocation{
				Destination:    request.Destination,
				Amount:         request.Amount[types.Address{}],
				AllocationType: outcome.GuaranteeAllocationType,
				Metadata:       guarantee,
			},
		},
	}}

	nextState.TurnNum = nextState.TurnNum + 1

	ss := state.NewSignedState(nextState)
	err = ss.SignAndAdd(secretKey)
	if err != nil {
		panic(err)
	}
	if ok := ledger.Channel.AddSignedState(ss); !ok {
		panic("could not add state")
	}

	messages := protocols.CreateSignedStateMessages(oId, ss, ledger.MyIndex)
	return protocols.SideEffects{MessagesToSend: messages}

}

// GetLedger returns the ledger for the given id.
// This will be deprecated in favour of using the store
func (l *LedgerCranker) GetLedger(ledgerId types.Destination) *channel.TwoPartyLedger {
	ledger, ok := l.ledgers[ledgerId]
	if !ok {
		panic(fmt.Sprintf("Ledger %s not found", ledgerId))
	}
	return ledger
}

func SignPreAndPostFundingStates(ledger *channel.TwoPartyLedger, secretKeys []*[]byte) {
	for _, sk := range secretKeys {
		_, _ = ledger.SignAndAddPrefund(sk)
	}
	for _, sk := range secretKeys {
		_, _ = ledger.Channel.SignAndAddPostfund(sk)
	}
}

func SignLatest(ledger *channel.TwoPartyLedger, secretKeys [][]byte) {

	// Find the largest turn num and therefore the latest state
	turnNum := uint64(0)
	for t := range ledger.SignedStateForTurnNum {
		if t > turnNum {
			turnNum = t
		}
	}
	// Sign it
	toSign := ledger.SignedStateForTurnNum[turnNum]
	for _, secretKey := range secretKeys {
		_ = toSign.SignAndAdd(&secretKey)
	}
	ledger.Channel.AddSignedState(toSign)
}
