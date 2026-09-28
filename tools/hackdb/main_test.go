package main

import "testing"

const sample = `## title:                   Pokemon Blue - 151
## rhdn_link:               http://www.romhacking.net/hacks/5742/
## released_by:             Razormime
## platform:                GB
## downloads:               646
## score:                   1/1
## category:                Improvement
## hack_of:                 Pokémon: Blue Version
## patch_ver:               1.2.1
## patching_info:           No Special Requirements
------------------------------------------
######## description ########
Pokémon Blue - 151 had a few simple goals.


######## rom_info ########
	* Database match: Pokemon - Blue Version (USA, Europe) (SGB Enhanced)
	* Database: No-Intro: Game Boy/Color (v. 20180815-131105)
	* File/ROM SHA-1: D7037C83E1AE5B39BDE3C30787637BA1D4C48CE2
	* File/ROM CRC32: D6DA8A1A
`

func TestParseInfo(t *testing.T) {
	in := parseInfo(sample)
	if in.Title != "Pokemon Blue - 151" || in.Downloads != 646 || in.HackOf != "Pokémon: Blue Version" ||
		in.ROMCRC != "D6DA8A1A" || in.FileSHA1 != "D7037C83E1AE5B39BDE3C30787637BA1D4C48CE2" ||
		in.DBMatch != "Pokemon - Blue Version (USA, Europe) (SGB Enhanced)" || in.Description != "Pokémon Blue - 151 had a few simple goals." {
		t.Fatalf("%+v", in)
	}
	sep := parseInfo("## title: X\n######## rom_info ########\n* File SHA-1: " + "AAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAA\n* File CRC32: 11111111\n* ROM SHA-1: BBBBBBBBBBBBBBBBBBBBBBBBBBBBBBBBBBBBBBBB\n* ROM CRC32: 22222222\n")
	if sep.FileCRC != "11111111" || sep.ROMCRC != "22222222" || sep.ROMSHA1[0] != 'B' {
		t.Fatalf("%+v", sep)
	}
}
