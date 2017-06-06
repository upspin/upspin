
$(document).ready(function() {

    //
    //
    // Code for /pickuser functionality
    //
    //

    // Toggles between picking a user and creating a new one
    $('input[name="user-picker-group"]').change(function() {
        if (this.id == "new-user") {
            $("#new-user-input").show()
            $("#next").attr("action", "/createuser")
        } else {
            $("#new-user-input").hide()
            $("#next").attr("action", "/setupdomain")
        }
    })

    // Appends the selected username to user/create.
    $('#next').submit(function(){
        if ($("#new-user-input").is(":visible")) {
                $('<input>').attr({
                type: 'hidden',
                value: $("#new-user-input").val(),
                name: 'username'
            }).appendTo('#next');
        } else if ($("#new-user-input").length) {
                $('<input>').attr({
                type: 'hidden',
                value: $('input[name="user-picker-group"]:checked').val(),
                name: 'userindex'
            }).appendTo('#next');
        }
    })

    //
    // Other code (TBD).
    //
})
