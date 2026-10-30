package main

// The zone database is embedded so the IANA time zone a delivery window names
// resolves on any base image, not only one that ships tzdata (ADR 0007).
import _ "time/tzdata"
