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

        stage('Docker Build & Push') {
            steps {
                withCredentials([usernamePassword(credentialsId: 'dockerhub-creds', usernameVariable: 'DOCKER_USER', passwordVariable: 'DOCKER_PASS')]) {
                    sh '''
                        export DOCKER_CONFIG=$WORKSPACE/.docker
                        mkdir -p $DOCKER_CONFIG

                        echo "$DOCKER_PASS" | /usr/local/bin/docker login -u "$DOCKER_USER" --password-stdin

                        pwd
                        ls -l

                        /usr/local/bin/docker build --platform linux/amd64 -f ./build/Dockerfile -t $DOCKER_IMAGE:latest .
                        /usr/local/bin/docker push $DOCKER_IMAGE:latest

                        rm -rf $DOCKER_CONFIG
                    '''
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
