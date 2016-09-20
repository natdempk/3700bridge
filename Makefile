all:
	$(RM) 3700bridge
	export GOPATH=${PWD}
	go build -o 3700bridge main.go

clean:
	$(RM) 3700bridge
