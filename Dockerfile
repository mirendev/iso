# Example Dockerfile for iso testing
FROM golang:1.23-alpine

# Install common testing tools
RUN apk add --no-cache \
    git \
    make \
    bash \
    curl

# Set working directory
WORKDIR /workspace

# Default command
CMD ["/bin/bash"]
