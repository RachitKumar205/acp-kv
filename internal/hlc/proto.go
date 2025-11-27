package hlc

import (
	"github.com/rachitkumar205/acp-kv/api/proto"
)

// convert hlc to proto format
func (h HLC) ToProto() *proto.HLC {
	return &proto.HLC{
		Physical: h.Physical,
		Logical:  h.Logical,
		NodeId:   h.NodeID,
	}
}

// convert proto hlc to internal format
func FromProto(p *proto.HLC) HLC {
	if p == nil {
		return HLC{}
	}
	return HLC{
		Physical: p.Physical,
		Logical:  p.Logical,
		NodeID:   p.NodeId,
	}
}
