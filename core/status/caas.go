// Copyright 2020 Canonical Ltd.
// Licensed under the AGPLv3, see LICENCE file for details.

package status

// UnitDisplayStatus is used for CAAS units where the status of the unit
// could be overridden by the status of the container.
func UnitDisplayStatus(unitStatus, containerStatus StatusInfo, expectWorkload bool) StatusInfo {
	if unitStatus.Status == Terminated {
		return unitStatus
	}
	if containerStatus.Status == Terminated {
		return containerStatus
	}
	if containerStatus.Status == "" {
		// No container update received from k8s yet.
		// Unit may have set status, (though final status
		// can only be active if a container status has come through).
		if isStatusModified(unitStatus) && (unitStatus.Status != Active || !expectWorkload) {
			return unitStatus
		}
		message := unitStatus.Message
		if expectWorkload {
			message = MessageWaitForContainer
		}

		// If no unit status set, assume still allocating.
		return StatusInfo{
			Status:  Waiting,
			Message: message,
			Since:   containerStatus.Since,
		}
	}
	if unitStatus.Status != Active && unitStatus.Status != Waiting && unitStatus.Status != Blocked {
		// Charm has said that there's a problem (error) or
		// it's doing something (maintenance) so we'll stick with that.
		return unitStatus
	}

	// Charm may think it's active, but as yet there's no way for it to
	// query the workload state, so we'll ensure that we only say that
	// it's active if the pod is reported as running. If not, we'll report
	// any pod error.
	switch containerStatus.Status {
	case Error, Blocked, Allocating:
		return containerStatus
	case Waiting:
		if unitStatus.Status == Active {
			return containerStatus
		}
	case Running:
		// Unit hasn't moved from initial state.
		// thumper: I find this questionable, at best it is Unknown.
		if !isStatusModified(unitStatus) {
			return containerStatus
		}
	}
	return unitStatus
}

// ApplicationDisplayStatus determines which of the two statuses to use when
// displaying application status in a CAAS model.
func ApplicationDisplayStatus(applicationStatus, operatorStatus StatusInfo, expectWorkload bool) StatusInfo {
	if applicationStatus.Status == Terminated {
		return applicationStatus
	}
	// Only interested in the operator status if it's not running/active.
	if operatorStatus.Status == Running || operatorStatus.Status == Active {
		return applicationStatus
	}

	if operatorStatus.Status == Waiting && !expectWorkload {
		operatorStatus.Message = MessageInitializingAgent
	}
	return operatorStatus

}

func isStatusModified(unitStatus StatusInfo) bool {
	return (unitStatus.Status != "" && unitStatus.Status != Waiting) ||
		(unitStatus.Message != MessageWaitForContainer && unitStatus.Message != MessageInitializingAgent)
}
