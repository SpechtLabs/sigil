ARG GO_VERSION=1.19
ARG ALPINE_VERSION=3.16

FROM golang:${GO_VERSION}-alpine${ALPINE_VERSION}

# CA certificates
RUN apk add -q --update --progress --no-cache ca-certificates make curl gpg dirmngr bash sudo bat file tzdata git mandoc git-doc openssh-client zsh zsh-vcs vim libstdc++ github-cli

ENV EDITOR=vim
ENV LANG=en_US.UTF-8
ENV TERM=xterm
ENV GO111MODULE=on

# install oh-my-zsh and my dotfiles
RUN mkdir -p /root/.config
RUN git clone --single-branch --depth 1 https://github.com/robbyrussell/oh-my-zsh.git ~/.oh-my-zsh
RUN git clone https://github.com/cedi/dotfiles.git /root/.config/dotfiles
RUN cd /root/.config/dotfiles/ && ./install.sh

RUN https://raw.githubusercontent.com/helm/helm/main/scripts/get-helm-3 | bash
RUN curl -sSfL https://raw.githubusercontent.com/golangci/golangci-lint/master/install.sh | sh -s -- -b $(go env GOPATH)/bin v1.49.0
RUN go install github.com/cweill/gotests/... && \
    go install github.com/swaggo/swag/cmd/swag@latest && \
    go install github.com/go-delve/delve/cmd/dlv@latest && \
    go install github.com/fatih/gomodifytags@latest && \
    go install github.com/vektra/mockery/v2@latest && \
    go install github.com/golang/mock/mockgen@v1.6.0 && \
    go install golang.org/x/tools/gopls@latest && \
    go install github.com/josharian/impl@latest && \
    go install github.com/uudashr/gopkgs/v2/cmd/gopkgs@latest


RUN curl -LO "https://dl.k8s.io/release/$(curl -L -s https://dl.k8s.io/release/stable.txt)/bin/linux/amd64/kubectl" && \
    sudo install -o root -g root -m 0755 kubectl /usr/local/bin/kubectl

RUN set -x; cd "$(mktemp -d)" && \
    OS="$(uname | tr '[:upper:]' '[:lower:]')" && \
    ARCH="$(uname -m | sed -e 's/x86_64/amd64/' -e 's/\(arm\)\(64\)\?.*/\1\2/' -e 's/aarch64$/arm64/')" && \
    KREW="krew-${OS}_${ARCH}" && \
    curl -fsSLO "https://github.com/kubernetes-sigs/krew/releases/latest/download/${KREW}.tar.gz" && \
    tar zxvf "${KREW}.tar.gz" && \
    ./"${KREW}" install krew

