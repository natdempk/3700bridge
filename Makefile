all:
	$(RM) 3700bridge
	export GOPATH=${PWD}
	go build -o 3700bridge main.go utils.go

clean:
	$(RM) 3700bridge
