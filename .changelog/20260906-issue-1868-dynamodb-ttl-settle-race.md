* [dynamodb] a TTL transition settling at the same moment as a new `UpdateTimeToLive` no longer overwrites it
  the settle for the previous ENABLING/DISABLING window could write the old specification back over an update accepted as the window closed, so a just-disabled table read ENABLED
  the settle now runs under the table's lock and only for the transition it was armed for
