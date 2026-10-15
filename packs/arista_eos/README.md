# arista_eos pack

Platform pack for Arista EOS, recorded and tested on cEOS 4.36.0F. How a pack is built is in
[docs/how-to/write-a-pack.md](../../docs/how-to/write-a-pack.md); the fact families it fills are in
[fact-families.md](../../specs/001-crawl-loop/contracts/fact-families.md).

## Known gaps

- OSPF authentication algorithm (feature 011): EOS prints md5 and sha256 message-digest
  authentication identically, in the text view (`Message-digest authentication, using key id 1`)
  and in the JSON view (`"authentication": "md5"` for both). `ospf_interfaces.auth_type` is
  therefore `message_digest` for either, and an md5/sha mismatch between two EOS devices is not
  visible in the rows.
