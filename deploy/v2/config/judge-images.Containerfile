FROM docker.io/library/alpine@sha256:1beb0dc0a51de7ff38e3b5274078a2e0b81113ba5c7535e1a03d5913a5edbda3 AS compiler

RUN /sbin/apk \
      --repositories-file /dev/null \
      --repository https://mirrors.ustc.edu.cn/alpine/v3.23/main \
      add --no-cache \
      binutils=2.45.1-r0 \
      g++=15.2.0-r2 \
      gcc=15.2.0-r2 \
      gmp=6.3.0-r4 \
      isl26=0.26-r1 \
      jansson=2.14.1-r0 \
      libatomic=15.2.0-r2 \
      libgcc=15.2.0-r2 \
      libgomp=15.2.0-r2 \
      libstdc++=15.2.0-r2 \
      libstdc++-dev=15.2.0-r2 \
      mpc1=1.3.1-r1 \
      mpfr4=4.2.2-r0 \
      musl-dev=1.2.5-r23 \
      zstd-libs=1.5.7-r2 \
    && /bin/rm -f /var/log/apk.log

ENTRYPOINT []
CMD []

FROM scratch AS runtime
