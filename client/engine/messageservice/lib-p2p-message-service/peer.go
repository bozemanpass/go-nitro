package libp2pms

import (
	"github.com/libp2p/go-libp2p/core/peer"
	"github.com/statechannels/go-nitro/types"
)

// PeerInfo contains information about a peer
type PeerInfo struct {
	Port      int64
	Id        peer.ID
	Address   types.Address
	IpAddress string
}
