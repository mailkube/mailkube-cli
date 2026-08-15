# The image is the binary and its trust store, and nothing else: no shell, no package manager, no
# interpreter. Nothing in the image can be made to run except the command you asked for.
#
# The binary is built by the release pipeline and copied in, rather than compiled here. The image
# therefore ships the same bytes as the archives, and the version it reports is the released one.
FROM gcr.io/distroless/static-debian12:nonroot

# One image definition covers every architecture. The release build stages each platform's binary
# under its own platform directory, and the build itself substitutes the one being built, so
# `linux/amd64` and `linux/arm64` come from the same three lines rather than from two files that
# have to be kept identical.
ARG TARGETPLATFORM
COPY ${TARGETPLATFORM}/mailkube /usr/bin/mailkube

# `webhooks listen` defaults to loopback, which a published container port cannot reach: traffic
# arriving on the host arrives on the container's external interface, not on its loopback one.
# Inside a container, pass `--host 0.0.0.0` and publish the port.
EXPOSE 4318

# Not root, and no shell to fall back to. `docker run … mailkube <command>` works exactly as the
# installed binary does, which is what keeps one set of documentation true for both.
USER nonroot:nonroot
ENTRYPOINT ["/usr/bin/mailkube"]
