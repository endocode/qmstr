# FASTEN Reporter

This reporter:

1. Queries license information collected by
the [scancode analyzer](../../analyzers/scancode-analyzer);
1. Creates a
[report](../../../proto/fasten.proto)
and sends it as a
[Kafka message](https://github.com/fasten-project/fasten/wiki/Kafka-Topics#fastenqmstr).
