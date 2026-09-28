# SNMP agent for the SNMPv3 integration tests (specs/006-snmpv3-protocols, research R7). Test only.
FROM alpine:3.22
RUN apk add --no-cache net-snmp net-snmp-tools && mkdir -p /var/lib/net-snmp
COPY snmpd.conf /etc/snmp/snmpd.conf
CMD ["snmpd", "-f", "-Lo", "-C", "-c", "/etc/snmp/snmpd.conf"]
