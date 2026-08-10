// ABOUTME: Embeds the IANA timezone database (issue #18) so timezone-aware
// ABOUTME: recurring schedules resolve zones without host zoneinfo files.

package server

import _ "time/tzdata"
