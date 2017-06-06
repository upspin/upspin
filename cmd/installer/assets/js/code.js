
$(document).ready(function() {

    $('input[name="user-picker-group').change(function() {
        if (this.id == "new-user") {
            $("#new-user-input").show()
            $("#next").attr("action", "/createuser")
        } else {
            $("#new-user-input").hide()
            $("#next").attr("action", "/setupdomain")
        }
    })

    $('#next').submit(function(){
        if ($("#new-user-input").is(":visible")) {
                $('<input>').attr({
                type: 'hidden',
                value: $("#new-user-input").val(),
                name: 'username'
            }).appendTo('#next');
        }
    })
})
