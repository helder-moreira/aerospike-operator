/*
Copyright 2018 The aerospike-operator Authors.

Licensed under the Apache License, Version 2.0 (the "License");
you may not use this file except in compliance with the License.
You may obtain a copy of the License at

    http://www.apache.org/licenses/LICENSE-2.0

Unless required by applicable law or agreed to in writing, software
distributed under the License is distributed on an "AS IS" BASIS,
WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied.
See the License for the specific language governing permissions and
limitations under the License.
*/

package versioning

// VersionUpgrade represents a transition between a source and a target Aerospike
// versions.
type VersionUpgrade struct {
	Source Version
	Target Version
}

// isDowngrade returns a boolean value indicating whether the current transition
// is a downgrade.
func (vu VersionUpgrade) isDowngrade() bool {
	return vu.Target.Major < vu.Source.Major ||
		vu.Target.Minor < vu.Source.Minor ||
		vu.Target.Patch < vu.Source.Patch ||
		vu.Target.Revision < vu.Target.Revision
}

// isMajorUpgrade returns a boolean value indicating whether the current
// transition is a major version upgrade.
func (vu VersionUpgrade) isMajorUpgrade() bool {
	return !vu.isDowngrade() && vu.Target.Major > vu.Source.Major
}

// isMinorUpgrade returns a boolean value indicating whether the current
// transition is a minor version upgrade.
func (vu VersionUpgrade) isMinorUpgrade() bool {
	return !vu.isDowngrade() && !vu.isMajorUpgrade() && vu.Target.Minor > vu.Source.Minor
}

// isPatchUpgrade returns a boolean value indicating whether the current
// transition is a patch version upgrade.
func (vu VersionUpgrade) isPatchUpgrade() bool {
	return !vu.isDowngrade() && !vu.isMajorUpgrade() && !vu.isMinorUpgrade() && vu.Target.Patch > vu.Source.Patch
}

// isPatchUpgrade returns a boolean value indicating whether the current
// transition is a revision version upgrade.
func (vu VersionUpgrade) isRevisionUpgrade() bool {
	return !vu.isDowngrade() && !vu.isMajorUpgrade() && !vu.isMinorUpgrade() && !vu.isPatchUpgrade() && vu.Target.Revision > vu.Source.Revision
}

// IsValid indicates whether the transition is valid. This initially means
// that the source and target versions are both well-known, supported versions,
// and that the transition is patch or revision only.
func (vu VersionUpgrade) IsValid() bool {
	return vu.Source.IsSupported() && vu.Target.IsSupported() && (vu.isPatchUpgrade() || vu.isRevisionUpgrade())
}
