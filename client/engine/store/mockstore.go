package store

import (
	"fmt"

	"github.com/statechannels/go-nitro/channel"
	"github.com/statechannels/go-nitro/crypto"
	"github.com/statechannels/go-nitro/protocols"
	"github.com/statechannels/go-nitro/protocols/directfund"
	"github.com/statechannels/go-nitro/protocols/virtualfund"
	"github.com/statechannels/go-nitro/types"
)

type MockStore struct {
	objectives map[protocols.ObjectiveId][]byte
	channels   map[types.Destination]channel.Channel

	key     []byte        // the signing key of the store's engine
	address types.Address // the (Ethereum) address associated to the signing key
}

func NewMockStore(key []byte) Store {
	ms := MockStore{}
	ms.key = key
	ms.address = crypto.GetAddressFromSecretKeyBytes(key)

	ms.objectives = make(map[protocols.ObjectiveId][]byte)
	ms.channels = make(map[types.Destination]channel.Channel)

	return &ms
}

func (ms MockStore) GetAddress() *types.Address {
	return &ms.address
}

func (ms MockStore) GetChannelSecretKey() *[]byte {
	return &ms.key
}

func (ms MockStore) GetObjectiveById(id protocols.ObjectiveId) (protocols.Objective, error) {
	// todo: locking
	objJSON, ok := ms.objectives[id]

	// return immediately if no such objective exists
	if !ok {
		return nil, fmt.Errorf("no objective with id %s exists in storage", id)
	}

	obj, err := decodeObjective(id, objJSON)
	if err != nil {
		return nil, fmt.Errorf("error decoding objective %s: %w", id, err)
	}

	obj, err = ms.populateChannelData(obj)
	if err != nil {
		// return existing objective data along with error
		return obj, fmt.Errorf("error populating channel data for objective %s: %w", id, err)
	}

	return obj, nil
}

func (ms MockStore) SetObjective(obj protocols.Objective) error {
	// todo: locking
	objJSON, err := obj.MarshalJSON()

	if err != nil {
		return fmt.Errorf("error setting objective %s: %w", obj.Id(), err)
	}

	ms.objectives[obj.Id()] = objJSON

	for _, ch := range obj.Channels() {
		err := ms.SetChannel(ch)
		if err != nil {
			return fmt.Errorf("error setting channel %s from objective %s: %w", ch.Id, obj.Id(), err)
		}
	}

	return nil
}

// SetChannel sets the channel in the store.
func (ms *MockStore) SetChannel(ch *channel.Channel) error {
	ms.channels[ch.Id] = *ch

	return nil // temp - errors can exist / be reported when serde reintroduced
}

// getChannelById returns the stored channel
func (ms *MockStore) getChannelById(id types.Destination) (channel.Channel, error) {
	ch, ok := ms.channels[id]
	if ok {
		return ch, nil
	} else {
		return channel.Channel{}, fmt.Errorf("channel %s not found", id)
	}
}

// GetTwoPartyLedger returns a ledger channel between the two parties if it exists.
func (ms MockStore) GetTwoPartyLedger(firstParty types.Address, secondParty types.Address) (ledger *channel.TwoPartyLedger, ok bool) {
	for _, ch := range ms.channels {
		if len(ch.Participants) == 2 {
			// TODO: Should order matter?
			if ch.Participants[0] == firstParty && ch.Participants[1] == secondParty {
				return &channel.TwoPartyLedger{Channel: ch}, true
			}
		}
	}
	return nil, false
}

func (ms MockStore) GetObjectiveByChannelId(channelId types.Destination) (protocols.Objective, bool) {
	// todo: locking
	for id, objJSON := range ms.objectives {
		obj, err := decodeObjective(id, objJSON)

		if err != nil {
			return nil, false
		}

		for _, ch := range obj.Channels() {
			if ch.Id == channelId {
				obj, err = ms.populateChannelData(obj)

				if err != nil {
					return nil, false // todo: enrich w/ err return
				}

				return obj, true
			}
		}
	}

	return nil, false
}

// populateChannelData fetches stored Channel data relevent to the given
// objective, attaches it to the objective, and returns the objective
func (ms MockStore) populateChannelData(obj protocols.Objective) (protocols.Objective, error) {
	id := obj.Id()

	if dfo, isDirectFund := obj.(*directfund.Objective); isDirectFund {

		ch, err := ms.getChannelById(dfo.C.Id)

		if err != nil {
			return nil, fmt.Errorf("error retrieving channel data for objective %s: %w", id, err)
		}

		dfo.C = &ch

		return dfo, nil

	} else if vfo, isVirtualFund := obj.(*virtualfund.Objective); isVirtualFund {

		v, err := ms.getChannelById(vfo.V.Id)
		if err != nil {
			return nil, fmt.Errorf("error retrieving virtual channel data for objective %s: %w", id, err)
		}
		vfo.V = &channel.SingleHopVirtualChannel{Channel: v}

		if vfo.ToMyLeft != nil && vfo.ToMyLeft.Channel != nil {
			left, err := ms.getChannelById(vfo.ToMyLeft.Channel.Id)
			if err != nil {
				return nil, fmt.Errorf("error retrieving left ledger channel data for objective %s: %w", id, err)
			}
			vfo.ToMyLeft.Channel = &channel.TwoPartyLedger{Channel: left}
		}

		if vfo.ToMyRight != nil && vfo.ToMyRight.Channel != nil {
			right, err := ms.getChannelById(vfo.ToMyRight.Channel.Id)
			if err != nil {
				return nil, fmt.Errorf("error retrieving right ledger channel data for objective %s: %w", id, err)
			}
			vfo.ToMyRight.Channel = &channel.TwoPartyLedger{Channel: right}

		}

		return vfo, nil

	} else {
		return nil, fmt.Errorf("objective %s did not correctly represent a known Objective type", id)
	}
}

// decodeObjective is a helper which encapsulates the deserialization
// of Objective JSON data.
func decodeObjective(id protocols.ObjectiveId, data []byte) (protocols.Objective, error) {
	if directfund.IsDirectFundObjective(id) {
		dfo := directfund.Objective{}
		err := dfo.UnmarshalJSON(data)

		return &dfo, err
	} else if virtualfund.IsVirtualFundObjective(id) {
		vfo := directfund.Objective{}
		err := vfo.UnmarshalJSON(data)

		return &vfo, err
	} else {
		return nil, fmt.Errorf("objective id %s does not correspond to a known Objective type", id)
	}
}
