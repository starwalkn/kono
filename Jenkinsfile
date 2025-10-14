pipeline {
    agent any

    environment {
        GO_VERSION = "1.23"
        APP_NAME = "kairyu"
        DOCKER_IMAGE = "starwalkn/kairyu"
    }

    stages {
        stage('Checkout') {
            steps {
                checkout scm
            }
        }

        stage('Setup Go') {
            steps {
                sh '''
                    curl -LO https://go.dev/dl/go${GO_VERSION}.linux-amd64.tar.gz
                    sudo tar -C /usr/local -xzf go${GO_VERSION}.linux-amd64.tar.gz
                    export PATH=$PATH:/usr/local/go/bin
                    go version
                '''
            }
        }

        stage('Build') {
            agent {
                docker { image 'golang:1.23-alpine' }
            }
            steps {
                sh 'go build -o /app/kairyu ./cmd/main'
            }
        }

        stage('Test') {
            steps {
                sh 'go test ./... -v'
            }
        }

        stage('Lint') {
            steps {
                sh '''
                    curl -sSfL https://raw.githubusercontent.com/golangci/golangci-lint/master/install.sh | sh -s -- -b $(go env GOPATH)/bin
                    golangci-lint run ./...
                '''
            }
        }

        stage('Docker Build & Push') {
            when {
                branch 'main'
            }
            steps {
                withCredentials([usernamePassword(credentialsId: 'dockerhub-creds', usernameVariable: 'DOCKER_USER', passwordVariable: 'DOCKER_PASS')]) {
                    sh '''
                        docker login -u "$DOCKER_USER" -p "$DOCKER_PASS"
                        docker build -t $DOCKER_IMAGE:latest -f build/docker/Dockerfile .
                        docker push $DOCKER_IMAGE:latest
                    '''
                }
            }
        }
    }

    post {
        success {
            echo "✅ Build completed successfully!"
        }
        failure {
            echo "❌ Build failed!"
        }
    }
}
