package prpc

type Server struct {
	server *ServiceManager
	client *CallManager
}
