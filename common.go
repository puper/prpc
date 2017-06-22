package prpc

type Header struct {
	IsResp        bool
	ServiceMethod string
	Seq           uint64
	Error         string
	next          *Header
}
