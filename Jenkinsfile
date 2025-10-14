pipeline {
    agent any

    environment {
        APP_NAME = "kairyu"
        DOCKER_IMAGE = "starwalkn/kairyu"
    }

    stages {

        stage('Checkout') {
            steps {
                checkout scm
            }
        }

        stage('Build Go binary') {
            steps {
                sh 'mkdir -p build'
                sh '/opt/homebrew/bin/go mod tidy'
                sh '/opt/homebrew/bin/go build -o build/kairyu ./cmd/main.go'
            }
        }

        stage('Docker Build & Push') {
            steps {
                withCredentials([usernamePassword(credentialsId: 'dockerhub-creds', usernameVariable: 'DOCKER_USER', passwordVariable: 'DOCKER_PASS')]) {
                    sh """
                        export DOCKER_CONFIG=$WORKSPACE/.docker
                        mkdir -p $DOCKER_CONFIG
                        echo "$DOCKER_PASS" | /usr/local/bin/docker login -u "$DOCKER_USER" --password-stdin
                        /usr/local/bin/docker build -t $DOCKER_IMAGE:latest .
                        /usr/local/bin/docker push $DOCKER_IMAGE:latest
                    """
                }
            }
        }

    }

    post {
        success {
            echo "✅ Docker image built and pushed successfully!"
        }
        failure {
            echo "❌ Build or push failed!"
        }
    }
}
