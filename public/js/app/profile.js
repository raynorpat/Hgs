/* Ordinary GET forms and links own profile navigation, including Back and refresh. */
(function () {
  "use strict";
  // Put an explicitly selected date in view without changing focus or the URL.
  var day = document.querySelector(".hg-calendar-day.is-selected");
  var region = document.querySelector(".hg-calendar-scroll");
  if (day && region) region.scrollLeft = Math.max(0, day.getBoundingClientRect().left - region.getBoundingClientRect().left + region.scrollLeft - region.clientWidth / 2);
}());
