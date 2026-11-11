# IOS-XE lab

A containerlab lab for the [cisco_iosxe pack](../../packs/cisco_iosxe) (feature 013): one Cat9kv,
`cat1`, between two Arista cEOS switches. `eos-a` is the seed; `eos-b` is known only from `cat1`'s
LLDP neighbours, so a crawl that reaches `eos-b` went through the Catalyst. The topology comment in
[iosxe.clab.yaml](iosxe.clab.yaml) says what each node and link is for. It runs next to `test/lab`
(own names, own management subnet `172.20.22.0/24`).

It runs on the NetLab VM, which has the images `vrnetlab/cisco_cat9kv:17.15.01` and
`ceos:4.36.0F` and no clone of this repository. The Cat9kv takes 18 GB of RAM and 4 vCPU; with the
28-node EVPN fabric running too, stop one of the two labs.

## Deploy and destroy

```sh
ssh NetLab mkdir -p netmapper-iosxe-lab
scp test/lab-iosxe/{iosxe.clab.yaml,cat1.cfg,eos-a.cfg,eos-b.cfg} NetLab:netmapper-iosxe-lab/
ssh NetLab
cd ~/netmapper-iosxe-lab
sudo containerlab deploy -t iosxe.clab.yaml
# ...
sudo containerlab destroy -t iosxe.clab.yaml --cleanup
```

## Wait for the Catalyst

`cat1` takes 12 to 16 minutes to be usable (measured on NetLab): about 12 and a half to vrnetlab's
`Startup complete`, then up to 3 more before its data ports come up. It is ready when both hold:

```sh
docker logs clab-netmapper-iosxe-cat1 2>&1 | grep "Startup complete"
docker exec clab-netmapper-iosxe-eos-a Cli -c "show lldp neighbors"   # cat1 on Et1 and Et2
```

Until then it is unreachable or its ports are `notconnect`: record nothing and crawl nothing.

## cat1.cfg

vrnetlab writes a bootstrap configuration (hostname, the `admin` account with password `admin`, the
vty lines, the `Mgmt-vrf` route and `GigabitEthernet0/0`), appends `cat1.cfg` to it, and IOS-XE
applies the whole file once, at first boot. So `cat1.cfg` must not configure `GigabitEthernet0/0`,
`Mgmt-vrf`, the vty lines or the `admin` account, and a change to it takes effect only on a new
deploy.

If the `netmapper` login is refused once `cat1` is ready, the bootstrap was applied without
`cat1.cfg`: log in as `admin`, read `show running-config`, fix `cat1.cfg` and redeploy with
`--reconfigure`.

The Arista nodes keep LLDP off on Management0: on the management network every node sees every
other one, and `eos-a` would learn `eos-b` without going through `cat1`. containerlab rewrites the
Management0 block of a cEOS startup-config, so the topology's `exec` applies it after deploy.
Check it before a crawl: `show lldp neighbors` on `eos-a` lists `cat1` on Et1 and Et2 and nothing
on Ma0.

## Record a fixture

Record from `cat1` as `netmapper` (privilege 1) in an interactive session with a tall pty, as the
collector does, one command per file, as
`packs/cisco_iosxe/testdata/lab/<device>_<template base>.raw`:

```sh
(sleep 5; echo "show interfaces"; sleep 8; echo exit) |
  script -qc "stty rows 32767 cols 200; sshpass -p admin ssh -tt netmapper@172.20.22.3" /dev/null |
  tr -d '\r'
```

Keep what lies between the command's echo (`cat1>show interfaces`) and the next prompt, and trim
each line's trailing spaces and the blank lines around the output: that is what the collector
stores. A configuration change for a variant is made as `admin`, then reverted.
