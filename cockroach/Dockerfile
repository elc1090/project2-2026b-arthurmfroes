FROM cockroachdb/cockroach:v23.2.0

COPY start.sh /cockroach/start.sh

RUN chmod +x /cockroach/start.sh

ENTRYPOINT ["/cockroach/start.sh"]
