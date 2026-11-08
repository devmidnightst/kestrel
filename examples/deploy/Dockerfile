# Two-stage image for a kestrel service: compile a static, hardened binary
# with the kestrel builder image, then ship only that binary.
#
#   docker build -t kestrel .                          (in the repository root)
#   docker build -t hello-service -f examples/deploy/Dockerfile examples/http
#   docker run --rm -p 8080:8080 hello-service
FROM kestrel AS build
COPY . /app
RUN kestrel build --release --static service.ks -o /app/server

FROM scratch
COPY --from=build /app/server /server
ENV HOST=0.0.0.0 PORT=8080
EXPOSE 8080
USER 65534:65534
ENTRYPOINT ["/server"]
